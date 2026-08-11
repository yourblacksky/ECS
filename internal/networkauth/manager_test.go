package networkauth

import (
	"os"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestIdentityPersistsAcrossRestarts(t *testing.T) {
	cfg := testConfig(t)
	first, err := New(cfg, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeID() == "" || first.NodeID() != second.NodeID() {
		t.Fatalf("node identity was not persisted: %q != %q", first.NodeID(), second.NodeID())
	}
	if first.PairingCode() != second.PairingCode() {
		t.Fatal("pairing code was not persisted")
	}
}

func TestLegacyRelationshipDefaultsToUpstreamExecutionEnabled(t *testing.T) {
	cfg := testConfig(t)
	manager, err := New(cfg, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.doc.Relationships = []Relationship{{
		EnrollmentID: "legacy", PeerNodeID: "ecs_0123456789abcdef0123456789abcdef",
		Role: RoleChild, Status: StatusActive, Token: "token",
	}}
	if err := manager.saveLocked(); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	manager.mu.Unlock()

	reloaded, err := New(cfg, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	if relationships := reloaded.List(); len(relationships) != 1 ||
		!relationships[0].AllowUpstreamExecute {
		t.Fatalf("legacy relationship did not preserve peer behavior: %#v", relationships)
	}
	persisted, err := os.ReadFile(reloaded.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"allow_upstream_execute": true`) {
		t.Fatalf("migration was not persisted: %s", persisted)
	}
}

func TestEnrollmentApprovalClaimHeartbeatAndRevocation(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities("ecs_0123456789abcdef0123456789abcdef", "child", "http://child.local")
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	capabilities.CapabilityHash = hash
	enrollment, err := parent.CreateJoin(JoinRequest{
		NodeID:         capabilities.NodeID,
		NodeName:       capabilities.Node,
		URL:            capabilities.URL,
		ParentNodeID:   parent.NodeID(),
		PairingCode:    "482913",
		Capabilities:   capabilities,
		CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	items := parent.List()
	if len(items) != 1 || items[0].Status != StatusPending ||
		items[0].PairingCode != "482913" || len(items[0].Capabilities.Tools) != 2 {
		t.Fatalf("unexpected pending relationship: %#v", items)
	}
	approved, err := parent.Approve(enrollment.EnrollmentID, Approval{
		Groups:        []string{"lab"},
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != StatusApproved {
		t.Fatalf("unexpected approval: %#v", approved)
	}
	claim, err := parent.Claim(enrollment.EnrollmentID, enrollment.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Status != StatusActive || claim.Token == "" ||
		len(claim.AllowedTools) != 1 || claim.AllowedTools[0] != "clock" {
		t.Fatalf("unexpected claim: %#v", claim)
	}
	if _, ok := parent.Authenticate(claim.Token, "reply:execute"); !ok {
		t.Fatal("active relationship token was not accepted")
	}
	if _, ok := parent.Authenticate(claim.Token, "trace:read"); ok {
		t.Fatal("relationship token exceeded its scopes")
	}
	parent.mu.Lock()
	parent.doc.Relationships[0].LastSeen = time.Now().UTC().Add(-10 * time.Second)
	parent.mu.Unlock()
	if _, ok := parent.Authenticate(claim.Token, "reply:execute"); ok {
		t.Fatal("expired relationship lease remained authorized")
	}
	heartbeat, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Status != StatusActive || !heartbeat.LeaseExpires.After(heartbeat.ServerTime) {
		t.Fatalf("unexpected heartbeat: %#v", heartbeat)
	}
	if _, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: "changed-without-manifest",
	}); err == nil {
		t.Fatal("changed capability hash without full manifest was accepted")
	}
	reduced := capabilities
	reduced.Tools = reduced.Tools[:1]
	reduced.Skills = reduced.Skills[:1]
	reducedHash, _ := CapabilityHash(reduced)
	reduced.CapabilityHash = reducedHash
	if _, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: reducedHash,
		Capabilities:   &reduced,
	}); err != nil {
		t.Fatal(err)
	}
	current := parent.List()[0]
	if len(current.AllowedTools) != 1 || current.AllowedTools[0] != "clock" {
		t.Fatalf("removed capabilities were not pruned from authorization: %#v", current.AllowedTools)
	}
	unavailable := reduced
	unavailable.Tools = nil
	unavailable.Skills = nil
	unavailable.Availability = []protocol.CapabilityAvailability{
		{Kind: "tool", Name: "clock", Available: false, Reason: "permission_denied"},
		{Kind: "skill", Name: "clock", Available: false, Reason: "tool_dependency_unavailable"},
	}
	unavailableHash, _ := CapabilityHash(unavailable)
	unavailable.CapabilityHash = unavailableHash
	if _, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: unavailableHash,
		Capabilities:   &unavailable,
	}); err != nil {
		t.Fatal(err)
	}
	current = parent.List()[0]
	if len(current.AllowedTools) != 1 || current.AllowedTools[0] != "clock" {
		t.Fatalf("temporary unavailability removed approved tool: %#v", current.AllowedTools)
	}
	if peers := parent.PeersFor("lab", []string{"clock"}, nil, "reply:execute"); len(peers) != 0 {
		t.Fatalf("temporarily unavailable tool remained schedulable: %#v", peers)
	}
	restoredHash, _ := CapabilityHash(reduced)
	reduced.CapabilityHash = restoredHash
	if _, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: restoredHash,
		Capabilities:   &reduced,
	}); err != nil {
		t.Fatal(err)
	}
	if peers := parent.PeersFor("lab", []string{"clock"}, nil, "reply:execute"); len(peers) != 1 {
		t.Fatalf("restored tool did not retain authorization: %#v", peers)
	}
	if _, err := parent.SetStatus(enrollment.EnrollmentID, StatusRevoked); err != nil {
		t.Fatal(err)
	}
	if _, ok := parent.Authenticate(claim.Token, "reply:execute"); ok {
		t.Fatal("revoked credential remained active")
	}
}

func TestApprovalCanExplicitlyDenyAllToolsAndSkills(t *testing.T) {
	manager, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities(
		"ecs_0123456789abcdef0123456789abcdef",
		"child",
		"http://child.local",
	)
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := manager.CreateJoin(JoinRequest{
		NodeID:         capabilities.NodeID,
		NodeName:       capabilities.Node,
		URL:            capabilities.URL,
		PairingCode:    "123456",
		Capabilities:   capabilities,
		CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := manager.Approve(enrollment.EnrollmentID, Approval{
		AllowedTools:  []string{},
		AllowedSkills: []string{},
		Scopes:        []string{"heartbeat:write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(approved.AllowedTools) != 0 || len(approved.AllowedSkills) != 0 {
		t.Fatalf("explicit empty capability policy was replaced with defaults: %#v", approved)
	}
}

func TestEnrollmentRejectsCapabilityIdentityAndHashMismatch(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities("ecs_0123456789abcdef0123456789abcdef", "child", "http://child.local")
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []JoinRequest{
		{
			NodeID: capabilities.NodeID, NodeName: "other", URL: capabilities.URL,
			PairingCode: "482913", Capabilities: capabilities, CapabilityHash: hash,
		},
		{
			NodeID: capabilities.NodeID, NodeName: capabilities.Node, URL: capabilities.URL,
			PairingCode: "482913", Capabilities: capabilities, CapabilityHash: "bad",
		},
	} {
		if _, err := parent.CreateJoin(request); err == nil {
			t.Fatalf("invalid enrollment was accepted: %#v", request)
		}
	}
}

func TestApprovalCannotAuthorizeUndeclaredCapabilities(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities("ecs_0123456789abcdef0123456789abcdef", "child", "http://child.local")
	hash, _ := CapabilityHash(capabilities)
	capabilities.CapabilityHash = hash
	enrollment, err := parent.CreateJoin(JoinRequest{
		NodeID: capabilities.NodeID, NodeName: capabilities.Node, URL: capabilities.URL,
		PairingCode: "482913", Capabilities: capabilities, CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Approve(enrollment.EnrollmentID, Approval{
		AllowedTools: []string{"shell"},
	}); err == nil {
		t.Fatal("undeclared tool authorization was accepted")
	}
}

func TestPermissionUpdateImmediatelyChangesAuthentication(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities("ecs_0123456789abcdef0123456789abcdef", "child", "http://child.local")
	hash, _ := CapabilityHash(capabilities)
	capabilities.CapabilityHash = hash
	enrollment, err := parent.CreateJoin(JoinRequest{
		NodeID: capabilities.NodeID, NodeName: capabilities.Node, URL: capabilities.URL,
		PairingCode: "482913", Capabilities: capabilities, CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Approve(enrollment.EnrollmentID, Approval{}); err != nil {
		t.Fatal(err)
	}
	claim, err := parent.Claim(enrollment.EnrollmentID, enrollment.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := parent.UpdatePermissions(enrollment.EnrollmentID, Approval{
		Groups:        []string{"recursive-workers"},
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
		Scopes:        []string{"heartbeat:write", "recursive:execute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("policy version was not advanced: %#v", updated)
	}
	if _, ok := parent.Authenticate(claim.Token, "reply:execute"); ok {
		t.Fatal("removed scope remained authorized on the next request")
	}
	if _, ok := parent.Authenticate(claim.Token, "recursive:execute"); ok {
		t.Fatal("rotated credential remained valid for execution")
	}
	heartbeat, err := parent.RecordHeartbeat(claim.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.PolicyVersion != 2 || len(heartbeat.Scopes) != 2 {
		t.Fatalf("heartbeat did not return the current policy: %#v", heartbeat)
	}
	if heartbeat.Token == "" || heartbeat.Token == claim.Token {
		t.Fatal("narrowed policy did not rotate the relationship credential")
	}
	if relationship, ok := parent.Authenticate(heartbeat.Token, "recursive:execute"); !ok ||
		relationship.Role != RoleChild {
		t.Fatalf("replacement credential was not authorized: %#v %v", relationship, ok)
	}
	if _, err := parent.RecordHeartbeat(heartbeat.Token, Heartbeat{
		NodeID:         capabilities.NodeID,
		CapabilityHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	if parent.List()[0].PolicyVersion != 2 {
		t.Fatal("credential acknowledgement changed the policy version")
	}
	if _, err := parent.UpdatePermissions(enrollment.EnrollmentID, Approval{
		Scopes: []string{"heartbeat:write", "unknown:scope"},
	}); err == nil {
		t.Fatal("unsupported scope was accepted")
	}
	if _, err := parent.UpdatePermissions(enrollment.EnrollmentID, Approval{
		Scopes: []string{"reply:execute"},
	}); err == nil {
		t.Fatal("policy without heartbeat scope was accepted")
	}
}

func TestPeersForRequiresDeclaredAndApprovedCapabilities(t *testing.T) {
	manager, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities(
		"ecs_0123456789abcdef0123456789abcdef",
		"child",
		"http://child.local",
	)
	manager.mu.Lock()
	manager.doc.Relationships = []Relationship{{
		PeerNodeID:    capabilities.NodeID,
		PeerName:      capabilities.Node,
		PeerURL:       capabilities.URL,
		Role:          RoleChild,
		Status:        StatusActive,
		Token:         "token",
		Groups:        []string{"workers"},
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
		Scopes:        []string{"reply:execute"},
		Capabilities:  capabilities,
		LastSeen:      time.Now().UTC(),
	}}
	manager.mu.Unlock()

	if peers := manager.PeersFor("workers", []string{"clock"}, []string{"clock"}, "reply:execute"); len(peers) != 1 {
		t.Fatalf("eligible peer was not returned: %#v", peers)
	}
	if peers := manager.PeersFor("workers", []string{"calculate"}, nil, "reply:execute"); len(peers) != 0 {
		t.Fatalf("unapproved capability was scheduled: %#v", peers)
	}
	if peers := manager.PeersFor("other", nil, nil, "reply:execute"); len(peers) != 0 {
		t.Fatalf("peer outside the group was scheduled: %#v", peers)
	}
	if peers := manager.PeersFor("workers", nil, nil, "recursive:execute"); len(peers) != 0 {
		t.Fatalf("peer without recursive scope was scheduled: %#v", peers)
	}
}

func TestUpstreamExecutionCanBeDisabledWithoutBreakingDownstream(t *testing.T) {
	manager, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	capabilities := testCapabilities(
		"ecs_0123456789abcdef0123456789abcdef",
		"child",
		"http://child.local",
	)
	manager.mu.Lock()
	manager.doc.Relationships = []Relationship{{
		PeerNodeID:           capabilities.NodeID,
		PeerName:             capabilities.Node,
		PeerURL:              capabilities.URL,
		Role:                 RoleChild,
		Status:               StatusActive,
		Token:                "child-token",
		Groups:               []string{"workers"},
		Scopes:               []string{"capabilities:read", "reply:execute"},
		AllowUpstreamExecute: &disabled,
		Capabilities:         capabilities,
		LastSeen:             time.Now().UTC(),
	}}
	manager.mu.Unlock()

	if _, ok := manager.Authenticate("child-token", "reply:execute"); ok {
		t.Fatal("child drove upstream execution while the explicit switch was off")
	}
	if _, ok := manager.Authenticate("child-token", "capabilities:read"); !ok {
		t.Fatal("read scope was incorrectly blocked by the execution switch")
	}
	if peers := manager.PeersFor("workers", nil, nil, "reply:execute"); len(peers) != 1 {
		t.Fatalf("controller could not dispatch downstream to its child: %#v", peers)
	}

	manager.mu.Lock()
	manager.doc.Relationships[0].Role = RoleController
	manager.mu.Unlock()
	if peers := manager.PeersFor("workers", nil, nil, "reply:execute"); len(peers) != 0 {
		t.Fatalf("disabled upstream controller remained schedulable: %#v", peers)
	}
}

func TestCapabilityPruningAdvancesPolicyVersion(t *testing.T) {
	manager, err := New(testConfig(t), "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities(
		"ecs_0123456789abcdef0123456789abcdef",
		"child",
		"http://child.local",
	)
	hash, _ := CapabilityHash(capabilities)
	manager.mu.Lock()
	manager.doc.Relationships = []Relationship{{
		PeerNodeID:     capabilities.NodeID,
		PeerName:       capabilities.Node,
		PeerURL:        capabilities.URL,
		Role:           RoleChild,
		Status:         StatusActive,
		Token:          "token",
		AllowedTools:   []string{"calculate", "clock"},
		AllowedSkills:  []string{"calculate", "clock"},
		Scopes:         []string{"heartbeat:write"},
		Capabilities:   capabilities,
		CapabilityHash: hash,
		PolicyVersion:  4,
		LastSeen:       time.Now().UTC(),
	}}
	manager.mu.Unlock()

	reduced := capabilities
	reduced.Tools = reduced.Tools[:1]
	reduced.Skills = reduced.Skills[:1]
	reducedHash, _ := CapabilityHash(reduced)
	response, err := manager.RecordHeartbeat("token", Heartbeat{
		NodeID: reduced.NodeID, CapabilityHash: reducedHash, Capabilities: &reduced,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.PolicyVersion != 5 {
		t.Fatalf("pruned authorization kept stale policy version: %#v", response)
	}
}

func testConfig(t *testing.T) config.NetworkAuthConfig {
	t.Helper()
	return config.NetworkAuthConfig{
		Enabled:           true,
		Directory:         t.TempDir(),
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}
}

func testCapabilities(nodeID, nodeName, nodeURL string) protocol.NodeCapabilities {
	return protocol.NodeCapabilities{
		Protocol: "ecs/1",
		NodeID:   nodeID,
		Node:     nodeName,
		URL:      nodeURL,
		Model:    "test-model",
		Features: []string{"relationship_token_rotation"},
		Tools: []protocol.ToolCapability{
			{Name: "clock", Version: "1.0.0"},
			{Name: "calculate", Version: "1.0.0"},
		},
		Skills: []protocol.SkillCapability{
			{Name: "clock", Version: "1.0.0"},
			{Name: "calculate", Version: "1.0.0"},
		},
	}
}
