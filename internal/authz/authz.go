package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

// Bounded is the only request type accepted by execution code. Its private
// field makes callers cross an explicit Trusted or Narrow authorization gate.
type Bounded struct {
	request protocol.ReplyRequest
}

// Trusted marks requests originating from local code or a static deployment
// credential. Call sites are intentionally easy to audit with a repository grep.
func Trusted(request protocol.ReplyRequest) Bounded {
	return Bounded{request: cloneRequest(request)}
}

// Narrow intersects a request with a relationship policy. Existing restriction
// flags are monotonic: an empty restricted list must remain empty at every hop.
func Narrow(
	relationship networkauth.PublicRelationship,
	request protocol.ReplyRequest,
) (Bounded, error) {
	request = cloneRequest(request)
	upstreamRestrictsTools := request.RestrictTools
	upstreamRestrictsSkills := request.RestrictSkills
	noSelection := len(request.Tools) == 0 && len(request.Skills) == 0

	if request.DirectTool != nil &&
		len(deniedCapabilities([]string{request.DirectTool.Name}, relationship.AllowedTools)) > 0 {
		return Bounded{}, errors.New("relationship does not authorize direct tool: " + request.DirectTool.Name)
	}

	if noSelection {
		if !upstreamRestrictsTools {
			request.Tools = append([]string(nil), relationship.AllowedTools...)
		}
		if !upstreamRestrictsSkills {
			request.Skills = append([]string(nil), relationship.AllowedSkills...)
		}
	} else {
		if denied := deniedCapabilities(request.Tools, relationship.AllowedTools); len(denied) > 0 {
			return Bounded{}, errors.New("relationship does not authorize tools: " + strings.Join(denied, ", "))
		}
		if denied := deniedCapabilities(request.Skills, relationship.AllowedSkills); len(denied) > 0 {
			return Bounded{}, errors.New("relationship does not authorize skills: " + strings.Join(denied, ", "))
		}
	}
	// Approval is an authority-bearing claim, not a capability selection. A
	// relationship caller may select only capabilities granted by policy; it
	// may never promote those selections to approved execution by setting a
	// JSON field on the request.
	request.ApprovedTools = nil

	request.RestrictTools = true
	request.RestrictSkills = true
	// The worker retains control over its local model.
	request.Model = ""
	// Pinning an agent principal is an identity choice, not a capability
	// selection: a relationship caller may never adopt an arbitrary agent
	// persona or its model via /v1/reply. Office execution reaches the same
	// field through NarrowApproved, which only runs on the signed/approved
	// worker path.
	request.Agent = ""
	if request.Metadata == nil {
		request.Metadata = make(map[string]string)
	}
	request.Metadata["ecs.auth.peer_node_id"] = relationship.PeerNodeID
	request.Metadata["ecs.auth.relationship_role"] = relationship.Role
	request.Metadata["ecs.auth.policy_version"] = fmt.Sprintf("%d", relationship.PolicyVersion)
	return Bounded{request: request}, nil
}

// NarrowApproved applies a relationship policy and then attaches approvals
// established by a trusted local verifier (for example, a signed Office
// execution grant). Callers must never pass values copied directly from an
// inbound ReplyRequest.
func NarrowApproved(
	relationship networkauth.PublicRelationship,
	request protocol.ReplyRequest,
	approved []string,
) (Bounded, error) {
	bounded, err := Narrow(relationship, request)
	if err != nil {
		return Bounded{}, err
	}
	approvalCapabilities := append(
		append([]string(nil), relationship.AllowedTools...),
		relationship.AllowedSkills...,
	)
	if denied := deniedCapabilities(approved, approvalCapabilities); len(denied) > 0 {
		return Bounded{}, errors.New("relationship cannot approve tools: " + strings.Join(denied, ", "))
	}
	// The Office worker path may pin a stage agent principal. Narrow strips
	// the field for /v1/reply, so restore the caller's explicit selection
	// here; capability constraints from both Narrow and the agent's own
	// allowlist still apply, so this cannot widen what the peer may execute.
	bounded.request.Agent = request.Agent
	bounded.request.ApprovedTools = append([]string(nil), approved...)
	return bounded, nil
}

// Request returns a defensive copy so authorization state cannot be mutated
// after it crosses the execution boundary.
func (b Bounded) Request() protocol.ReplyRequest {
	return cloneRequest(b.request)
}

func deniedCapabilities(requested, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	var denied []string
	for _, name := range requested {
		if !allowedSet[name] {
			denied = append(denied, name)
		}
	}
	return denied
}

func cloneRequest(request protocol.ReplyRequest) protocol.ReplyRequest {
	request.Messages = append([]protocol.Message(nil), request.Messages...)
	request.Skills = append([]string(nil), request.Skills...)
	request.Tools = append([]string(nil), request.Tools...)
	request.RequiredSkills = append([]string(nil), request.RequiredSkills...)
	request.RequiredTools = append([]string(nil), request.RequiredTools...)
	request.ApprovedTools = append([]string(nil), request.ApprovedTools...)
	if request.Metadata != nil {
		metadata := request.Metadata
		request.Metadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			request.Metadata[key] = value
		}
	}
	if request.DirectTool != nil {
		copyOfTool := *request.DirectTool
		copyOfTool.Arguments = append(json.RawMessage(nil), request.DirectTool.Arguments...)
		request.DirectTool = &copyOfTool
	}
	return request
}
