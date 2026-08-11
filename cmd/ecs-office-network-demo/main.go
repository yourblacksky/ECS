// ecs-office-network-demo drives the complete four-stage meeting follow-up
// loop (scope -> extract -> verify -> dispatch) across the three-node ECS
// demo: the controller creates the work order and dispatches stages to
// worker-a/worker-b through the office group, the dispatch stage pauses at
// the human approval gate until an admin approves it, and the second dispatch
// completes the loop. It is the network counterpart of
// ecs-agentteams-testbed and must not be used against production nodes.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"ecs/internal/office"
)

func main() {
	controllerURL := flag.String("controller", "http://127.0.0.1:18090", "controller URL")
	token := flag.String("token", "demo-controller-token", "controller auth token")
	adminToken := flag.String("admin-token", "demo-admin-token", "controller admin token")
	group := flag.String("group", "demo", "office dispatch group")
	timeout := flag.Duration("timeout", 60*time.Second, "overall run timeout")
	flag.Parse()
	if *timeout <= 0 {
		fatal(fmt.Errorf("timeout must be positive"))
	}

	client := &http.Client{Timeout: 20 * time.Second}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := checkHealth(ctx, client, *controllerURL); err != nil {
		fatal(err)
	}

	order, err := createWorkOrder(ctx, client, *controllerURL, *token)
	if err != nil {
		fatal(err)
	}
	orderID := order.ID

	// First dispatch executes scope/extract/verify across the workers and
	// stops at the dispatch stage's human approval gate.
	pending, err := dispatchWorkOrder(ctx, client, *controllerURL, *token, *group, orderID)
	if err != nil {
		fatal(err)
	}
	if pending.Status == "completed" {
		fatal(fmt.Errorf("work order %s completed without the approval gate", orderID))
	}
	approval := dispatchStageApproval(pending)
	if approval == nil {
		fatal(fmt.Errorf("dispatch stage has no pending approval record"))
	}
	if err := approveStage(ctx, client, *controllerURL, *adminToken, orderID, approval.ID); err != nil {
		fatal(err)
	}

	// Second dispatch resumes the approved dispatch stage and completes it.
	completed, err := dispatchWorkOrder(ctx, client, *controllerURL, *token, *group, orderID)
	if err != nil {
		fatal(err)
	}
	if err := verifyEvidence(completed); err != nil {
		fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]any{
		"status":            "ok",
		"work_order":        orderID,
		"nodes":             []string{"ecs-controller", "ecs-worker-a", "ecs-worker-b"},
		"group":             *group,
		"stages":            stageSummaries(completed),
		"approval":          approval.ID,
		"external_receipts": len(dispatchStage(completed).Receipts),
		"proof":             "three independent ECS HTTP processes executed the four-stage meeting follow-up loop",
		"ran_within":        time.Since(started).String(),
	}, "", "  ")
	fmt.Println(string(output))
}

func createWorkOrder(ctx context.Context, client *http.Client, controllerURL string, token string) (office.WorkOrder, error) {
	var order office.WorkOrder
	source := "决定：本周完成可信会议督办演示\n" +
		"行动：整理五分钟演示清单 | owner=张三 | due=2026-08-03 | evidence=会议记录第2页\n" +
		"行动：核对回执证据 | owner=李四 | due=2026-08-04 | evidence=会议记录第3页"
	err := postJSON(ctx, client, controllerURL+"/v1/office/components/meeting_followup/work-orders", token, map[string]any{
		"requested_by": "ecs-office-network-demo",
		"input": map[string]any{
			"meeting_title": "AgentTeams ECS E2E",
			"source":        source,
		},
	}, &order)
	return order, err
}

func dispatchWorkOrder(ctx context.Context, client *http.Client, controllerURL string, token string, group string, orderID string) (office.WorkOrder, error) {
	var order office.WorkOrder
	endpoint := controllerURL + "/v1/office/work-orders/" + orderID + "/dispatch"
	body, _ := json.Marshal(map[string]string{"group": group})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return order, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return order, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return order, err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return order, fmt.Errorf("%s returned %s: %s", endpoint, response.Status, data)
	}
	if err := json.Unmarshal(data, &order); err != nil {
		return order, fmt.Errorf("decode dispatch response: %w", err)
	}
	return order, nil
}

func approveStage(ctx context.Context, client *http.Client, controllerURL string, adminToken string, orderID string, approvalID string) error {
	body, _ := json.Marshal(map[string]any{
		"approval_id": approvalID, "decision": "approve", "actor": "QA负责人", "opinion": "三节点固定成功路径批准",
	})
	endpoint := controllerURL + "/v1/admin/office/work-orders/" + orderID + "/tasks/dispatch/approval"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s: %s", endpoint, response.Status, data)
	}
	return nil
}

func checkHealth(ctx context.Context, client *http.Client, controllerURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, controllerURL+"/healthz", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("controller health check failed: %s", response.Status)
	}
	return nil
}

func dispatchStageApproval(order office.WorkOrder) *office.ApprovalRecord {
	for _, task := range order.Tasks {
		if task.StageID == "dispatch" && task.ApprovalRecord != nil {
			return task.ApprovalRecord
		}
	}
	return nil
}

func dispatchStage(order office.WorkOrder) office.Task {
	for _, task := range order.Tasks {
		if task.StageID == "dispatch" {
			return task
		}
	}
	return office.Task{}
}

func verifyEvidence(order office.WorkOrder) error {
	if order.Status != "completed" {
		return fmt.Errorf("work order did not complete: %s", order.Status)
	}
	workerNames := map[string]bool{"ecs-worker-a": true, "ecs-worker-b": true}
	for _, stage := range []string{"scope", "extract", "verify", "dispatch"} {
		var task office.Task
		for _, candidate := range order.Tasks {
			if candidate.StageID == stage {
				task = candidate
				break
			}
		}
		if task.Status != "completed" {
			return fmt.Errorf("stage %s is not completed: %s", stage, task.Status)
		}
		if len(task.Attempts) == 0 || task.Attempts[len(task.Attempts)-1].Status != office.AttemptSucceeded {
			return fmt.Errorf("stage %s has no succeeded attempt", stage)
		}
		if node := task.Attempts[len(task.Attempts)-1].Node; !workerNames[node] {
			return fmt.Errorf("stage %s was not executed on a worker node: %q", stage, node)
		}
		if stage == "dispatch" {
			record := task.ApprovalRecord
			if record == nil || record.Status != "approved" || record.DecidedBy != "QA负责人" {
				return fmt.Errorf("dispatch approval gate was not recorded: %#v", record)
			}
			if len(task.Receipts) == 0 {
				return fmt.Errorf("dispatch stage produced no external receipts")
			}
			if task.ReceiptVerification == nil || task.ReceiptVerification.Status != "verified" {
				return fmt.Errorf("dispatch receipts were not verified")
			}
		}
	}
	return nil
}

func stageSummaries(order office.WorkOrder) map[string]any {
	summary := make(map[string]any)
	for _, task := range order.Tasks {
		node := ""
		status := task.Status
		if len(task.Attempts) > 0 {
			node = task.Attempts[len(task.Attempts)-1].Node
		}
		summary[task.StageID] = map[string]any{"status": status, "node": node}
	}
	return summary
}

func postJSON(ctx context.Context, client *http.Client, url string, token string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return fmt.Errorf("%s returned %s: %s", url, response.Status, data)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "office network demo failed:", err)
	os.Exit(1)
}
