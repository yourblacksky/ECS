package networkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestChildAutomaticallyEnrollsClaimsAndHeartbeats(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/network/join", func(w http.ResponseWriter, r *http.Request) {
		var request JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := parent.CreateJoin(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("POST /v1/network/enrollments/{enrollment}/claim", func(w http.ResponseWriter, r *http.Request) {
		response, err := parent.Claim(
			r.PathValue("enrollment"),
			strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("POST /v1/network/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var heartbeat Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := parent.RecordHeartbeat(
			strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			heartbeat,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	parent.nodeURL = server.URL

	childConfig := config.NetworkAuthConfig{
		Enabled:           true,
		Directory:         t.TempDir(),
		Parent:            config.ParentConfig{URL: server.URL, PairingCode: "654321"},
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}
	child, err := New(childConfig, "child", "http://child.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := testCapabilities(child.NodeID(), "child", "http://child.local")
	client := server.Client()
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	pending := parent.List()
	if len(pending) != 1 || pending[0].Status != StatusPending ||
		pending[0].PairingCode != "654321" {
		t.Fatalf("child did not create a pending enrollment: %#v", pending)
	}
	if _, err := parent.Approve(pending[0].EnrollmentID, Approval{}); err != nil {
		t.Fatal(err)
	}
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	if parent.List()[0].Status != StatusActive {
		t.Fatalf("parent did not receive an active heartbeat: %#v", parent.List()[0])
	}
	parentFileBefore, err := os.ReadFile(parent.path)
	if err != nil {
		t.Fatal(err)
	}
	childFileBefore, err := os.ReadFile(child.path)
	if err != nil {
		t.Fatal(err)
	}
	parentLastSeen := parent.List()[0].LastSeen
	childLastSeen := child.List()[0].LastSeen
	time.Sleep(time.Millisecond)
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	parentFileAfter, err := os.ReadFile(parent.path)
	if err != nil {
		t.Fatal(err)
	}
	childFileAfter, err := os.ReadFile(child.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parentFileBefore, parentFileAfter) {
		t.Fatal("stable heartbeat rewrote the parent's relationship file")
	}
	if !bytes.Equal(childFileBefore, childFileAfter) {
		t.Fatal("stable heartbeat rewrote the child's relationship file")
	}
	if !parent.List()[0].LastSeen.After(parentLastSeen) ||
		!child.List()[0].LastSeen.After(childLastSeen) {
		t.Fatal("stable heartbeat did not refresh the in-memory lease")
	}
	childRelationships := child.List()
	if len(childRelationships) != 1 ||
		childRelationships[0].Role != RoleController ||
		childRelationships[0].Status != StatusActive {
		t.Fatalf("child did not install its controller relationship: %#v", childRelationships)
	}
	child.mu.Lock()
	originalRelationshipToken := child.doc.Relationships[0].Token
	child.mu.Unlock()
	updated, err := parent.UpdatePermissions(pending[0].EnrollmentID, Approval{
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
		Scopes:        []string{"heartbeat:write", "recursive:execute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	childRelationship := child.List()[0]
	if childRelationship.PolicyVersion != updated.PolicyVersion ||
		len(childRelationship.Scopes) != 2 {
		t.Fatalf("child did not synchronize the updated policy: %#v", childRelationship)
	}
	parent.mu.Lock()
	relationshipToken := parent.doc.Relationships[0].Token
	parent.mu.Unlock()
	if relationshipToken == originalRelationshipToken {
		t.Fatal("narrowed policy did not rotate the shared relationship token")
	}
	parentPeers := parent.PeersFor("all", nil, nil, "recursive:execute")
	if len(parentPeers) != 1 || parentPeers[0].Token != originalRelationshipToken {
		t.Fatalf("parent stopped using the transition token before acknowledgement: peers=%#v relationships=%#v", parentPeers, parent.List())
	}
	if _, ok := child.Authenticate(originalRelationshipToken, "recursive:execute"); !ok {
		t.Fatal("child rejected downstream work during credential transition")
	}
	if _, ok := parent.Authenticate(relationshipToken, "reply:execute"); ok {
		t.Fatal("child-to-parent request inherited a removed permission")
	}
	if _, ok := child.Authenticate(relationshipToken, "reply:execute"); ok {
		t.Fatal("parent-to-child request inherited a removed permission")
	}
	if relation, ok := parent.Authenticate(relationshipToken, "recursive:execute"); !ok ||
		relation.Role != RoleChild {
		t.Fatal("child-to-parent request was not independently re-authorized")
	}
	if relation, ok := child.Authenticate(relationshipToken, "recursive:execute"); !ok ||
		relation.Role != RoleController {
		t.Fatal("parent-to-child request was not independently re-authorized")
	}
	if err := child.syncParent(context.Background(), client, func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	if peers := parent.PeersFor("all", nil, nil, "recursive:execute"); len(peers) != 1 ||
		peers[0].Token != relationshipToken {
		t.Fatalf("parent did not finish credential rotation: %#v", peers)
	}
	if _, ok := child.Authenticate(originalRelationshipToken, "recursive:execute"); ok {
		t.Fatal("transition credential remained valid after acknowledgement")
	}
}

func TestChildDiscoversParentAndSubmitsPendingEnrollment(t *testing.T) {
	parent, err := New(testConfig(t), "parent", "")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/network/join", func(w http.ResponseWriter, r *http.Request) {
		var request JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := parent.CreateJoin(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(response)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	parent.nodeURL = server.URL

	childConfig := config.NetworkAuthConfig{
		Enabled:   true,
		Directory: t.TempDir(),
		Parent: config.ParentConfig{
			AutoDiscover:    true,
			DiscoveryGroups: []string{"controllers"},
			PairingCode:     "246810",
		},
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}
	child, err := New(childConfig, "child", "http://child.local")
	if err != nil {
		t.Fatal(err)
	}
	child.SetDiscoverySource(func() []protocol.NodeCapabilities {
		return []protocol.NodeCapabilities{
			{
				NodeID:   "ecs_ffffffffffffffffffffffffffffffff",
				Node:     "ignored",
				URL:      "http://ignored.local",
				Groups:   []string{"other"},
				Features: []string{"network_auth"},
			},
			{
				NodeID:   parent.NodeID(),
				Node:     "parent",
				URL:      server.URL,
				Groups:   []string{"controllers"},
				Features: []string{"network_auth"},
				Platform: eligibleParentPlatform(),
			},
		}
	})
	capabilities := testCapabilities(child.NodeID(), "child", "http://child.local")
	if err := child.syncParent(context.Background(), server.Client(), func() protocol.NodeCapabilities {
		return capabilities
	}); err != nil {
		t.Fatal(err)
	}
	pending := parent.List()
	if len(pending) != 1 || pending[0].Status != StatusPending ||
		pending[0].PeerNodeID != child.NodeID() ||
		pending[0].PairingCode != "246810" {
		t.Fatalf("discovery did not create the expected pending enrollment: %#v", pending)
	}
	_, childPending := child.parentState()
	if childPending == nil || childPending.URL != server.URL ||
		childPending.NodeID != parent.NodeID() {
		t.Fatalf("child did not persist the discovered parent: %#v", childPending)
	}
}

func TestAutomaticParentElectionPreventsReciprocalEnrollment(t *testing.T) {
	cfg := config.NetworkAuthConfig{
		Enabled:           true,
		Directory:         t.TempDir(),
		Parent:            config.ParentConfig{AutoDiscover: true},
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}
	manager, err := New(cfg, "candidate", "http://candidate.local")
	if err != nil {
		t.Fatal(err)
	}
	const lowerNodeID = "ecs_00000000000000000000000000000000"
	const higherNodeID = "ecs_ffffffffffffffffffffffffffffffff"
	manager.SetDiscoverySource(func() []protocol.NodeCapabilities {
		return []protocol.NodeCapabilities{
			{
				NodeID: higherNodeID, URL: "http://higher.local",
				Features: []string{"network_auth"}, Platform: eligibleParentPlatform(),
			},
			{
				NodeID: lowerNodeID, URL: "http://lower.local",
				Features: []string{"network_auth"}, Platform: eligibleParentPlatform(),
			},
		}
	})
	target, err := manager.resolveParent(protocol.NodeCapabilities{
		NodeID: manager.NodeID(),
		Platform: protocol.PlatformCapabilities{
			AcceptsChildren: true,
			SupportsInbound: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.NodeID != lowerNodeID {
		t.Fatalf("expected the lower node_id to win parent election, got %#v", target)
	}
}

func TestLeafChoosesEligibleParentRegardlessOfNodeIDOrder(t *testing.T) {
	cfg := config.NetworkAuthConfig{
		Enabled:           true,
		Directory:         t.TempDir(),
		Parent:            config.ParentConfig{AutoDiscover: true},
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}
	manager, err := New(cfg, "leaf", "http://leaf.local")
	if err != nil {
		t.Fatal(err)
	}
	const higherNodeID = "ecs_ffffffffffffffffffffffffffffffff"
	manager.SetDiscoverySource(func() []protocol.NodeCapabilities {
		return []protocol.NodeCapabilities{{
			NodeID:   higherNodeID,
			URL:      "http://controller.local",
			Features: []string{"network_auth"},
			Platform: eligibleParentPlatform(),
		}}
	})
	target, err := manager.resolveParent(protocol.NodeCapabilities{
		NodeID: manager.NodeID(),
		Platform: protocol.PlatformCapabilities{
			TopologyRole:    "leaf",
			AcceptsChildren: false,
			SupportsInbound: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.NodeID != higherNodeID {
		t.Fatalf("leaf did not choose the eligible controller: %#v", target)
	}
}

func TestAutomaticParentRequiresChildrenAndInboundCapabilities(t *testing.T) {
	const candidateNodeID = "ecs_00000000000000000000000000000000"
	tests := []struct {
		name     string
		platform protocol.PlatformCapabilities
		want     bool
	}{
		{
			name: "eligible",
			platform: protocol.PlatformCapabilities{
				AcceptsChildren: true,
				SupportsInbound: true,
			},
			want: true,
		},
		{
			name: "does not accept children",
			platform: protocol.PlatformCapabilities{
				SupportsInbound: true,
			},
		},
		{
			name: "does not support inbound",
			platform: protocol.PlatformCapabilities{
				AcceptsChildren: true,
			},
		},
		{
			name: "legacy capabilities omitted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := New(config.NetworkAuthConfig{
				Enabled:   true,
				Directory: t.TempDir(),
				Parent: config.ParentConfig{
					NodeID:       candidateNodeID,
					AutoDiscover: true,
				},
				HeartbeatInterval: config.Duration(time.Second),
				LeaseTimeout:      config.Duration(3 * time.Second),
				EnrollmentTTL:     config.Duration(10 * time.Minute),
			}, "candidate", "http://candidate.local")
			if err != nil {
				t.Fatal(err)
			}
			manager.SetDiscoverySource(func() []protocol.NodeCapabilities {
				return []protocol.NodeCapabilities{{
					NodeID:   candidateNodeID,
					Node:     "parent",
					URL:      "http://parent.local",
					Features: []string{"network_auth"},
					Platform: test.platform,
				}}
			})
			target, err := manager.resolveParent(protocol.NodeCapabilities{NodeID: manager.NodeID()})
			if test.want {
				if err != nil || target.NodeID != candidateNodeID {
					t.Fatalf("eligible parent was rejected: %#v %v", target, err)
				}
				return
			}
			if !errors.Is(err, errNoDiscoveredParent) {
				t.Fatalf("ineligible parent returned error %v", err)
			}
		})
	}
}

func eligibleParentPlatform() protocol.PlatformCapabilities {
	return protocol.PlatformCapabilities{
		AcceptsChildren: true,
		SupportsInbound: true,
	}
}
