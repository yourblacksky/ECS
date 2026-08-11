package mobile

import (
	"context"
	"errors"
	"strings"

	"ecs/internal/platform"
)

const deviceCapabilityCommand = "@device"

type deviceCapabilityBridge struct {
	provider DeviceCapabilityProvider
}

func (b deviceCapabilityBridge) ToolAvailability(
	request platform.ToolRequest,
) platform.ToolAvailability {
	if request.Command != deviceCapabilityCommand || len(request.Args) != 1 {
		return platform.ToolAvailability{Reason: "unsupported_capability"}
	}
	if b.provider == nil {
		return platform.ToolAvailability{Reason: "provider_unavailable"}
	}
	reason := strings.TrimSpace(b.provider.Availability(request.Args[0]))
	return platform.ToolAvailability{Available: reason == "", Reason: reason}
}

func (b deviceCapabilityBridge) Execute(
	ctx context.Context,
	request platform.ToolRequest,
) (platform.ToolResult, error) {
	if request.Command != deviceCapabilityCommand || len(request.Args) != 1 {
		return platform.ToolResult{}, errors.New("unsupported mobile device capability")
	}
	if err := ctx.Err(); err != nil {
		return platform.ToolResult{}, err
	}
	name := request.Args[0]
	availability := b.ToolAvailability(request)
	if !availability.Available {
		return platform.ToolResult{}, errors.New("device capability is unavailable: " + availability.Reason)
	}
	output, err := b.provider.Execute(name, string(request.Input))
	if err != nil {
		return platform.ToolResult{Stderr: err.Error()}, err
	}
	if err := ctx.Err(); err != nil {
		return platform.ToolResult{Stdout: output}, err
	}
	output = strings.TrimSpace(output)
	if request.MaxOutputBytes > 0 && int64(len(output)) > request.MaxOutputBytes {
		return platform.ToolResult{}, errors.New("device tool output exceeds configured limit")
	}
	return platform.ToolResult{Stdout: output}, nil
}
