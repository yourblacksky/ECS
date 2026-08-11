package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ecs/internal/office"
	"ecs/internal/trace"
)

type officeDispatchRequest struct {
	Group string `json:"group"`
	Node  string `json:"node,omitempty"`
}

type officeApprovalDecisionRequest struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Actor      string `json:"actor,omitempty"`
	Opinion    string `json:"opinion,omitempty"`
}

type officeTaskExecutionRequest struct {
	RequestID          string                 `json:"request_id"`
	TraceID            string                 `json:"trace_id"`
	WorkOrder          string                 `json:"work_order"`
	Component          string                 `json:"component"`
	TaskID             string                 `json:"task_id"`
	TaskName           string                 `json:"task_name"`
	Agent              string                 `json:"agent"`
	AgentRole          string                 `json:"agent_role"`
	Prompt             string                 `json:"prompt"`
	Skills             []string               `json:"skills,omitempty"`
	Tools              []string               `json:"tools"`
	Connectors         []string               `json:"connectors,omitempty"`
	Approval           *office.ApprovalRecord `json:"approval,omitempty"`
	ApprovalGrant      *officeExecutionGrant  `json:"approval_grant,omitempty"`
	IdempotencyKey     string                 `json:"idempotency_key,omitempty"`
	AcceptanceCriteria []string               `json:"acceptance_criteria,omitempty"`
	Context            map[string]string      `json:"context,omitempty"`
}

type officeExecutionGrant struct {
	Version   string `json:"version"`
	Issuer    string `json:"issuer"`
	Subject   string `json:"subject"`
	Signature string `json:"signature"`
}

type officeCompensationRequest struct {
	WorkOrder      string                    `json:"work_order"`
	TaskID         string                    `json:"task_id"`
	Action         string                    `json:"action"`
	Connector      string                    `json:"connector"`
	Tool           string                    `json:"tool"`
	IdempotencyKey string                    `json:"idempotency_key"`
	Receipts       []office.ExecutionReceipt `json:"receipts,omitempty"`
}

type officeCompensationResponse struct {
	Status   string                    `json:"status"`
	Tool     string                    `json:"tool"`
	Receipts []office.ExecutionReceipt `json:"receipts,omitempty"`
}

func (s *Server) listOfficeComponents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.office.Capabilities())
}

func (s *Server) listOfficeWorkOrders(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	orders, err := s.office.ListOrders(
		strings.TrimSpace(r.URL.Query().Get("component")),
		strings.TrimSpace(r.URL.Query().Get("status")),
		requestOwner(r.Context()),
		limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *Server) createOfficeWorkOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request office.CompileRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid office work order: "+err.Error())
		return
	}
	if !trace.IsValidID(request.TraceID) {
		request.TraceID = trace.NewID()
	}
	request.OwnerPeerNodeID = requestOwner(r.Context())
	component := r.PathValue("component")
	recorder := s.traces.StartOwned(
		request.OwnerPeerNodeID,
		request.TraceID, request.TraceID, s.cfg.NodeName,
		"/v1/office/components/{component}/work-orders",
		map[string]any{"component": component, "requested_by": request.RequestedBy},
	)
	if recorder.TraceID() != "" {
		request.TraceID = recorder.TraceID()
	}
	order, err := s.office.Compile(component, request)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			recorder.Finish("error", "office component not found", err)
			writeError(w, http.StatusNotFound, "office component not found")
			return
		}
		recorder.Finish("rejected", "office work order rejected", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, task := range order.Tasks {
		recorder.Event(recorder.RootID(), "office_task", task.Name, "planned", map[string]any{
			"task_id": task.ID, "agent": task.Agent, "agent_role": task.AgentRole,
			"depends_on": task.DependsOn, "skills": task.Skills, "connectors": task.Connectors,
			"approval": task.Approval, "on_failure": task.OnFailure,
		})
	}
	recorder.Finish("planned", "office work order "+order.ID+" planned", nil)
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) getOfficeWorkOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.office.LoadOrder(r.PathValue("order"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "office work order not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !office.OwnedBy(order, requestOwner(r.Context())) {
		writeError(w, http.StatusNotFound, "office work order not found")
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) decideOfficeTaskApproval(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request officeApprovalDecisionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Office approval decision: "+err.Error())
		return
	}
	request.ApprovalID = strings.TrimSpace(request.ApprovalID)
	request.Decision = strings.ToLower(strings.TrimSpace(request.Decision))
	request.Actor = strings.TrimSpace(request.Actor)
	request.Opinion = strings.TrimSpace(request.Opinion)
	if request.ApprovalID == "" || (request.Decision != "approve" && request.Decision != "reject") {
		writeError(w, http.StatusBadRequest, "approval_id and decision (approve or reject) are required")
		return
	}
	if len([]rune(request.Actor)) > 128 || len([]rune(request.Opinion)) > 2000 {
		writeError(w, http.StatusBadRequest, "actor or opinion is too long")
		return
	}
	order, err := s.office.DecideTaskApproval(
		r.PathValue("order"), r.PathValue("task"), request.ApprovalID,
		request.Decision, request.Actor, request.Opinion, time.Now().UTC(),
	)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			writeError(w, http.StatusNotFound, "Office work order not found")
		case errors.Is(err, office.ErrApprovalExpired):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "approval_expired", "work_order": order,
			})
		case errors.Is(err, office.ErrApprovalAlreadyDecided):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, order)
}
