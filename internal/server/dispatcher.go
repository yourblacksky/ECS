package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ecs/internal/browsernode"
	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

type peerDispatcher interface {
	Dispatch(context.Context, config.Peer, protocol.ReplyRequest) protocol.PeerResult
}

func normalizeExpectedText(request *protocol.ReplyRequest) error {
	if request.ExpectedText == "" {
		return nil
	}
	if len([]rune(request.ExpectedText)) > 512 {
		return fmt.Errorf("expected_text must not exceed 512 characters")
	}
	if request.DirectTool != nil || len(request.Tools) != 0 || len(request.Skills) != 0 ||
		len(request.ApprovedTools) != 0 {
		return fmt.Errorf("expected_text cannot be combined with tools, skills, or approvals")
	}
	return nil
}

func normalizeDirectTool(request *protocol.ReplyRequest) error {
	if request.DirectTool == nil {
		return nil
	}
	request.DirectTool.Name = strings.TrimSpace(request.DirectTool.Name)
	if request.DirectTool.Name == "" {
		return fmt.Errorf("direct_tool.name is required")
	}
	if len(request.DirectTool.Arguments) == 0 {
		request.DirectTool.Arguments = json.RawMessage(`{}`)
	}
	var arguments map[string]any
	if err := json.Unmarshal(request.DirectTool.Arguments, &arguments); err != nil || arguments == nil {
		return fmt.Errorf("direct_tool.arguments must be a JSON object")
	}
	if len(request.Skills) != 0 {
		return fmt.Errorf("direct_tool cannot be combined with skills")
	}
	if len(request.Tools) > 1 || (len(request.Tools) == 1 && request.Tools[0] != request.DirectTool.Name) {
		return fmt.Errorf("direct_tool must be the only requested tool")
	}
	request.Tools = []string{request.DirectTool.Name}
	return nil
}

type httpPeerDispatcher struct {
	server *Server
}

func (d httpPeerDispatcher) Dispatch(
	ctx context.Context,
	peer config.Peer,
	request protocol.ReplyRequest,
) protocol.PeerResult {
	return d.server.callPeer(ctx, peer, request)
}

type browserPeerDispatcher struct {
	runtime browserRuntime
	timeout time.Duration
}

func (d browserPeerDispatcher) Dispatch(
	ctx context.Context,
	peer config.Peer,
	request protocol.ReplyRequest,
) protocol.PeerResult {
	result := protocol.PeerResult{Peer: peer.Name}
	if d.runtime == nil {
		result.Error = "browser task runtime is unavailable"
		return result
	}
	if request.DirectTool == nil {
		result.Error = "browser dispatch requires direct_tool"
		return result
	}
	task, err := d.runtime.Dispatch(ctx, peer.NodeID, browsernode.TaskRequest{
		Tool:      request.DirectTool.Name,
		Arguments: request.DirectTool.Arguments,
		Timeout:   d.timeout,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if task.Error != "" {
		result.Error = task.Error
		return result
	}
	output := strings.TrimSpace(string(task.Result))
	if output == "" {
		output = "null"
	}
	content := output
	var text string
	if json.Unmarshal(task.Result, &text) == nil {
		content = text
	}
	var durationMS int64
	if duration, parseErr := time.ParseDuration(task.Duration); parseErr == nil {
		durationMS = duration.Milliseconds()
	}
	result.Response = &protocol.ReplyResponse{
		RequestID: request.RequestID,
		TraceID:   request.TraceID,
		Node:      peer.Name,
		Message: protocol.Message{
			Role:    "assistant",
			Content: content,
		},
		ToolRuns: []protocol.ToolRun{{
			Name:      request.DirectTool.Name,
			Arguments: append(json.RawMessage(nil), request.DirectTool.Arguments...),
			Output:    output,
			Duration:  durationMS,
		}},
	}
	return result
}

type unavailablePeerDispatcher struct {
	transport string
}

func (d unavailablePeerDispatcher) Dispatch(
	_ context.Context,
	peer config.Peer,
	_ protocol.ReplyRequest,
) protocol.PeerResult {
	return protocol.PeerResult{
		Peer:  peer.Name,
		Error: "unsupported peer transport " + d.transport,
	}
}

func (s *Server) dispatcherFor(peer config.Peer) peerDispatcher {
	switch peer.Transport {
	case "", networkauth.TransportHTTP:
		return httpPeerDispatcher{server: s}
	case networkauth.TransportBrowserWebSocket:
		timeout := s.cfg.HTTP.PeerTimeout.Duration()
		if timeout <= 0 || timeout > 60*time.Second {
			timeout = 30 * time.Second
		}
		return browserPeerDispatcher{runtime: s.browserHub, timeout: timeout}
	default:
		return unavailablePeerDispatcher{transport: peer.Transport}
	}
}
