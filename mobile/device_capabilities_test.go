package mobile

import (
	"context"
	"errors"
	"testing"

	"ecs/internal/platform"
)

type stubDeviceCapabilities struct {
	unavailableReason string
	output            string
	err               error
}

func (s stubDeviceCapabilities) Availability(string) string {
	return s.unavailableReason
}

func (s stubDeviceCapabilities) Execute(string, string) (string, error) {
	return s.output, s.err
}

func TestDeviceCapabilityBridgeExecutesNativeCapability(t *testing.T) {
	bridge := deviceCapabilityBridge{provider: stubDeviceCapabilities{
		output: `{"state":"on"}`,
	}}
	result, err := bridge.Execute(context.Background(), platform.ToolRequest{
		Command:        deviceCapabilityCommand,
		Args:           []string{"flashlight"},
		Input:          []byte(`{"action":"on"}`),
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != `{"state":"on"}` {
		t.Fatalf("unexpected output: %s", result.Stdout)
	}
}

func TestDeviceCapabilityBridgeRejectsUnavailableCapability(t *testing.T) {
	bridge := deviceCapabilityBridge{provider: stubDeviceCapabilities{
		unavailableReason: "permission_denied",
	}}
	status := bridge.ToolAvailability(platform.ToolRequest{
		Command: deviceCapabilityCommand,
		Args:    []string{"flashlight"},
	})
	if status.Available || status.Reason != "permission_denied" {
		t.Fatalf("unexpected availability: %#v", status)
	}
	_, err := bridge.Execute(context.Background(), platform.ToolRequest{
		Command: deviceCapabilityCommand,
		Args:    []string{"flashlight"},
	})
	if err == nil {
		t.Fatal("unavailable device capability was accepted")
	}
}

func TestDeviceCapabilityBridgeReturnsNativeError(t *testing.T) {
	expected := errors.New("permission denied")
	bridge := deviceCapabilityBridge{provider: stubDeviceCapabilities{
		err: expected,
	}}
	result, err := bridge.Execute(context.Background(), platform.ToolRequest{
		Command: deviceCapabilityCommand,
		Args:    []string{"flashlight"},
	})
	if !errors.Is(err, expected) || result.Stderr != expected.Error() {
		t.Fatalf("unexpected native error: result=%#v err=%v", result, err)
	}
}
