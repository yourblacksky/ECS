//go:build agentteams_upstream_e2e

package service

import (
	"context"
	"os"
	"strings"
	"testing"

	v1beta1 "github.com/agentscope-ai/AgentTeams/agentteams-controller/api/v1beta1"
	"github.com/agentscope-ai/AgentTeams/agentteams-controller/internal/oss/ossfake"
	"sigs.k8s.io/yaml"
)

// TestECSRuntimeProjectionEvidence runs inside the pinned, patched AgentTeams
// checkout. It exercises the real Controller Deployer and writes the exact
// runtime.yaml subsequently consumed by the ECS process in the E2E script.
func TestECSRuntimeProjectionEvidence(t *testing.T) {
	output := strings.TrimSpace(os.Getenv("ECS_AGENTTEAMS_RUNTIME_OUTPUT"))
	if output == "" {
		t.Fatal("ECS_AGENTTEAMS_RUNTIME_OUTPUT is required")
	}
	store := ossfake.NewMemory()
	deployer := NewDeployer(DeployerConfig{
		OSS: store,
		RuntimeProjection: RuntimeProjectionConfig{
			StorageProvider: "minio",
			StorageBucket:   "agentteams-e2e",
			StorageEndpoint: "http://storage.agentteams.test",
			AIGatewayURL:    "http://gateway.agentteams.test/v1",
		},
	})
	spec := v1beta1.WorkerSpec{
		Runtime:  "ecs",
		Model:    "ecs-e2e-model",
		Identity: "ECS trusted Office worker",
		Soul:     "Keep the assigned scope deterministic.",
		Agents:   "Execute only the AgentTeams task assigned to this worker.",
		Skills:   []string{"meeting.fact.extract", "meeting.evidence.verify"},
	}
	err := deployer.DeployMemberRuntimeConfig(context.Background(), MemberRuntimeConfigDeployRequest{
		Name:              "ecs-worker-1",
		RuntimeName:       "ecs-worker-1",
		Runtime:           "ecs",
		Role:              "worker",
		Generation:        1,
		Spec:              spec,
		MatrixUserID:      "@ecs-worker-1:matrix.test",
		PersonalRoomID:    "!ecs-worker-1:matrix.test",
		MatrixAccessToken: "matrix-e2e-token",
		TeamName:          "ecs-office-team",
		TeamRoomID:        "!ecs-e2e:matrix.test",
		LeaderName:        "agentteams-manager",
		LeaderRuntimeName: "agentteams-manager",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := store.GetObject(context.Background(), "agents/ecs-worker-1/runtime/runtime.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatalf("invalid runtime projection: %v", err)
	}
	member, _ := document["member"].(map[string]any)
	desired, _ := document["desired"].(map[string]any)
	if member["runtime"] != "ecs" || member["runtimeName"] != "ecs-worker-1" {
		t.Fatalf("unexpected ECS member projection: %#v", member)
	}
	if desired["identity"] != spec.Identity || desired["soul"] != spec.Soul ||
		desired["instructions"] != spec.Agents {
		t.Fatalf("ECS instructions were not projected: %#v", desired)
	}
	if err := os.WriteFile(output, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}
