package meetingdemo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFixedMeetingFollowupScenariosFiveTimes(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	runner, err := New(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signatures := make(map[string]string)
	for iteration := 1; iteration <= 5; iteration++ {
		for _, scenario := range []string{"success", "failure"} {
			evidence, err := runner.Run(context.Background(), scenario, iteration)
			if err != nil {
				t.Fatalf("%s iteration %d: %v", scenario, iteration, err)
			}
			if len(evidence.Agents) < 3 || len(evidence.Stages) != 4 {
				t.Fatalf("%s has incomplete collaboration evidence: %#v", scenario, evidence)
			}
			if len(evidence.ExecutionChain) < 3 {
				t.Fatalf("%s has no Stage-Skill-Tool evidence chain: %#v", scenario, evidence)
			}
			switch scenario {
			case "success":
				if evidence.Status != "completed" || evidence.Approval == nil ||
					evidence.Approval.Status != "approved" ||
					!evidence.ExternalActionExecuted || len(evidence.Receipts) == 0 ||
					evidence.ReceiptVerification == nil ||
					evidence.ReceiptVerification.Status != "verified" {
					t.Fatalf("success path is incomplete: %#v", evidence)
				}
			case "failure":
				if evidence.Status != "blocked" || len(evidence.Blockers) == 0 ||
					evidence.ExternalActionExecuted || len(evidence.Receipts) != 0 ||
					evidence.Approval != nil {
					t.Fatalf("failure path did not block before side effects: %#v", evidence)
				}
			}
			if previous := signatures[scenario]; previous != "" &&
				previous != evidence.StableSignature {
				t.Fatalf("%s path changed across runs:\n%s\n%s", scenario, previous, evidence.StableSignature)
			}
			signatures[scenario] = evidence.StableSignature
		}
	}
}

func TestReferenceEvidenceMatchesManifest(t *testing.T) {
	referenceRoot := filepath.Join("..", "..", "evidence", "meeting-followup", "reference")
	manifestData, err := os.ReadFile(filepath.Join(referenceRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion  string            `json:"schema_version"`
		SourceRevision string            `json:"source_revision"`
		Files          map[string]string `json:"files"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "ecs.meeting-demo-reference/v1" || len(manifest.SourceRevision) != 40 {
		t.Fatalf("invalid reference manifest provenance: %#v", manifest)
	}
	for relative, expected := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(referenceRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(data))
		if actual != expected {
			t.Fatalf("reference evidence %s checksum changed: got %s, want %s", relative, actual, expected)
		}
	}

	for _, expected := range []struct {
		file     string
		scenario string
		status   string
		executed bool
		receipts bool
		approval bool
		blocked  bool
	}{
		{file: "success.json", scenario: "success", status: "completed", executed: true, receipts: true, approval: true},
		{file: "failure.json", scenario: "failure", status: "blocked", blocked: true},
	} {
		data, err := os.ReadFile(filepath.Join(referenceRoot, expected.file))
		if err != nil {
			t.Fatal(err)
		}
		var evidence Evidence
		if err := json.Unmarshal(data, &evidence); err != nil {
			t.Fatalf("decode %s: %v", expected.file, err)
		}
		if evidence.Scenario != expected.scenario || evidence.Status != expected.status ||
			evidence.ExternalActionExecuted != expected.executed ||
			(len(evidence.Receipts) > 0) != expected.receipts ||
			(evidence.Approval != nil) != expected.approval ||
			(len(evidence.Blockers) > 0) != expected.blocked {
			t.Fatalf("reference evidence %s has unexpected result: %#v", expected.file, evidence)
		}
	}
}
