package authz

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

func TestRestrictedEmptyCapabilitiesSurviveWireAndAnotherHop(t *testing.T) {
	first, err := Narrow(networkauth.PublicRelationship{
		PeerNodeID:    "peer-a",
		AllowedTools:  []string{},
		AllowedSkills: []string{},
	}, protocol.ReplyRequest{Prompt: "do work"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(first.Request())
	if err != nil {
		t.Fatal(err)
	}
	var onWire protocol.ReplyRequest
	if err := json.Unmarshal(encoded, &onWire); err != nil {
		t.Fatal(err)
	}
	if !onWire.RestrictTools || !onWire.RestrictSkills {
		t.Fatalf("restriction flags were lost on the wire: %s", encoded)
	}
	second, err := Narrow(networkauth.PublicRelationship{
		PeerNodeID:    "peer-b",
		AllowedTools:  []string{"calculate"},
		AllowedSkills: []string{"calculate"},
	}, onWire)
	if err != nil {
		t.Fatal(err)
	}
	request := second.Request()
	if len(request.Tools) != 0 || len(request.Skills) != 0 {
		t.Fatalf("a later hop widened empty restrictions: %#v", request)
	}
}

func TestNarrowRejectsCapabilitiesOutsideRelationship(t *testing.T) {
	_, err := Narrow(networkauth.PublicRelationship{
		AllowedTools: []string{"clock"},
	}, protocol.ReplyRequest{Prompt: "calculate", Tools: []string{"calculate"}})
	if err == nil {
		t.Fatal("unauthorized tool was accepted")
	}
}

func TestNarrowPreservesIndependentToolAndSkillRestrictions(t *testing.T) {
	bounded, err := Narrow(networkauth.PublicRelationship{
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"calculate"},
	}, protocol.ReplyRequest{
		Prompt:         "do work",
		RestrictTools:  true,
		RestrictSkills: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := bounded.Request()
	if len(request.Tools) != 0 {
		t.Fatalf("restricted empty tool list was widened: %#v", request.Tools)
	}
	if len(request.Skills) != 1 || request.Skills[0] != "calculate" {
		t.Fatalf("unrestricted skills were not narrowed to relationship policy: %#v", request.Skills)
	}
}

func TestNarrowStripsCallerSuppliedApprovals(t *testing.T) {
	bounded, err := Narrow(networkauth.PublicRelationship{
		AllowedTools:  []string{"connector_tool"},
		AllowedSkills: []string{"office.action.dispatch"},
	}, protocol.ReplyRequest{
		Prompt: "dispatch", Skills: []string{"office.action.dispatch"},
		Tools:         []string{"connector_tool"},
		ApprovedTools: []string{"office.action.dispatch", "connector_tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Request().ApprovedTools) != 0 {
		t.Fatalf("caller supplied approvals crossed the relationship boundary: %#v", bounded.Request())
	}
}

func TestNarrowApprovedAttachesOnlyVerifiedRelationshipCapabilities(t *testing.T) {
	relationship := networkauth.PublicRelationship{
		AllowedTools: []string{"connector_tool"}, AllowedSkills: []string{"dispatch_skill"},
	}
	bounded, err := NarrowApproved(relationship, protocol.ReplyRequest{
		Prompt: "dispatch", Tools: []string{"connector_tool"}, Skills: []string{"dispatch_skill"},
	}, []string{"connector_tool", "dispatch_skill"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Request().ApprovedTools) != 2 {
		t.Fatalf("verified approvals were not attached: %#v", bounded.Request())
	}
	if _, err := NarrowApproved(relationship, protocol.ReplyRequest{Prompt: "dispatch"}, []string{"admin"}); err == nil {
		t.Fatal("approval outside relationship policy was accepted")
	}
}

func TestTrustedExecutionBoundariesAreExplicit(t *testing.T) {
	allowed := map[string]bool{
		filepath.Join("server", "network.go"): true,
	}
	found := make(map[string]bool)
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "authz.Trusted(") {
			return nil
		}
		relative, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		found[relative] = true
		if !allowed[relative] {
			t.Errorf("new trusted execution boundary requires review: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path := range allowed {
		if !found[path] {
			t.Errorf("expected trusted execution boundary moved or disappeared: %s", path)
		}
	}
}

func TestNarrowStripsCallerSuppliedAgentPrincipal(t *testing.T) {
	relationship := networkauth.PublicRelationship{
		AllowedTools: []string{"connector_tool"}, AllowedSkills: []string{"dispatch_skill"},
	}
	bounded, err := Narrow(relationship, protocol.ReplyRequest{
		Prompt: "dispatch", Agent: "decision_reviewer",
		Tools: []string{"connector_tool"}, Skills: []string{"dispatch_skill"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent := bounded.Request().Agent; agent != "" {
		t.Fatalf("/v1/reply peer pinned an agent principal: %q", agent)
	}
}

func TestNarrowApprovedPreservesAgentForOfficeWorkerPath(t *testing.T) {
	relationship := networkauth.PublicRelationship{
		AllowedTools: []string{"meeting_fact_extract"}, AllowedSkills: []string{"meeting.fact.extract"},
	}
	bounded, err := NarrowApproved(relationship, protocol.ReplyRequest{
		Prompt: "extract", Agent: "minutes_clerk",
		Tools: []string{"meeting_fact_extract"}, Skills: []string{"meeting.fact.extract"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if agent := bounded.Request().Agent; agent != "minutes_clerk" {
		t.Fatalf("office worker path lost its stage agent principal: %q", agent)
	}
}
