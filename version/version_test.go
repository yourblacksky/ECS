package version

import (
	"strings"
	"testing"
)

func TestCurrentSemanticVersion(t *testing.T) {
	if Current() != "0.3.0" {
		t.Fatalf("current version = %q", Current())
	}
	if value := String(); !strings.HasPrefix(value, "ecs v0.3.0") || !strings.Contains(value, " go=") {
		t.Fatalf("unexpected version string: %q", value)
	}
}
