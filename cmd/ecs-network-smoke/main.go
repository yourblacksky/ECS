package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	"ecs/internal/protocol"
	"ecs/internal/trace"
)

type target struct {
	name  string
	url   string
	token string
}

func main() {
	controllerURL := flag.String("controller", "http://127.0.0.1:18090", "controller URL")
	workerAURL := flag.String("worker-a", "http://127.0.0.1:18091", "worker A URL")
	workerBURL := flag.String("worker-b", "http://127.0.0.1:18092", "worker B URL")
	flag.Parse()

	client := &http.Client{Timeout: 20 * time.Second}
	nodes := []target{
		{name: "ecs-controller", url: *controllerURL, token: "demo-controller-token"},
		{name: "ecs-worker-a", url: *workerAURL, token: "demo-worker-a-token"},
		{name: "ecs-worker-b", url: *workerBURL, token: "demo-worker-b-token"},
	}
	for _, node := range nodes {
		if err := checkHealth(client, node.url); err != nil {
			fatal(err)
		}
		if node.name != "ecs-controller" {
			if err := checkDirectReply(client, node); err != nil {
				fatal(err)
			}
		}
	}
	groupNodes, groupTraceID, err := checkGroupBroadcast(client, nodes[0])
	if err != nil {
		fatal(err)
	}
	evidenceNodes, err := checkAggregatedTrace(client, nodes[0], groupTraceID)
	if err != nil {
		fatal(err)
	}
	if err := checkObservability(client, nodes[0], groupTraceID); err != nil {
		fatal(err)
	}
	recursiveNodes, err := checkRecursive(client, nodes[0])
	if err != nil {
		fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]any{
		"status":          "ok",
		"nodes":           []string{"ecs-controller", "ecs-worker-a", "ecs-worker-b"},
		"group_broadcast": groupNodes,
		"evidence_tree":   evidenceNodes,
		"recursive":       recursiveNodes,
		"proof":           "three independent ECS HTTP processes executed clock",
	}, "", "  ")
	fmt.Println(string(output))
}

func checkHealth(client *http.Client, baseURL string) error {
	response, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s/healthz returned %s", baseURL, response.Status)
	}
	return nil
}

func checkDirectReply(client *http.Client, node target) error {
	request := protocol.ReplyRequest{
		RequestID: "smoke-direct-" + node.name,
		Prompt:    "读取本节点时间",
		DirectTool: &protocol.ToolInvocation{
			Name: "clock", Arguments: json.RawMessage(`{}`),
		},
	}
	var response protocol.ReplyResponse
	if err := postJSON(client, node.url+"/v1/reply", node.token, request, &response); err != nil {
		return err
	}
	if response.Node != node.name || !hasSuccessfulTool(response.ToolRuns, "clock") {
		return fmt.Errorf("%s did not execute clock: %#v", node.name, response)
	}
	return nil
}

func checkGroupBroadcast(client *http.Client, controller target) ([]string, string, error) {
	request := protocol.ReplyRequest{
		RequestID:    "smoke-group-broadcast",
		Prompt:       "让两个执行节点读取本机时间",
		DispatchMode: "broadcast",
		DirectTool: &protocol.ToolInvocation{
			Name: "clock", Arguments: json.RawMessage(`{}`),
		},
	}
	var response protocol.GroupResponse
	if err := postJSON(
		client,
		controller.url+"/v1/groups/demo/reply",
		controller.token,
		request,
		&response,
	); err != nil {
		return nil, "", err
	}
	if response.DispatchMode != "broadcast" || len(response.Results) != 2 {
		return nil, "", fmt.Errorf("unexpected group response: %#v", response)
	}
	nodes := make([]string, 0, len(response.Results))
	for _, result := range response.Results {
		if result.Error != "" || result.Response == nil ||
			!hasSuccessfulTool(result.Response.ToolRuns, "clock") {
			return nil, "", fmt.Errorf("group peer failed: %#v", result)
		}
		nodes = append(nodes, result.Response.Node)
	}
	sort.Strings(nodes)
	if !sameNodes(nodes) {
		return nil, "", fmt.Errorf("group did not reach both workers: %#v", nodes)
	}
	if response.TraceID == "" {
		return nil, "", errors.New("group response did not expose a trace id")
	}
	return nodes, response.TraceID, nil
}

func checkAggregatedTrace(
	client *http.Client,
	controller target,
	traceID string,
) ([]string, error) {
	var bundle trace.EvidenceBundle
	if err := getJSON(
		client,
		controller.url+"/v1/traces/"+traceID+"?aggregate=true",
		controller.token,
		&bundle,
	); err != nil {
		return nil, err
	}
	if bundle.Root.Trace == nil || bundle.Root.Node != controller.name ||
		len(bundle.Root.Children) != 2 {
		return nil, fmt.Errorf("unexpected evidence bundle: %#v", bundle)
	}
	nodes := make([]string, 0, 3)
	nodes = append(nodes, bundle.Root.Node)
	for _, child := range bundle.Root.Children {
		if child.Error != "" || child.Trace == nil {
			return nil, fmt.Errorf("evidence child could not be resolved: %#v", child)
		}
		nodes = append(nodes, child.Node)
	}
	sort.Strings(nodes)
	expected := []string{"ecs-controller", "ecs-worker-a", "ecs-worker-b"}
	for index := range expected {
		if nodes[index] != expected[index] {
			return nil, fmt.Errorf("unexpected evidence nodes: %#v", nodes)
		}
	}
	return nodes, nil
}

func checkObservability(
	client *http.Client,
	controller target,
	traceID string,
) error {
	var traces struct {
		Count  int             `json:"count"`
		Traces []trace.Summary `json:"traces"`
	}
	if err := getJSON(
		client,
		controller.url+"/v1/traces?limit=20",
		controller.token,
		&traces,
	); err != nil {
		return err
	}
	found := false
	for _, item := range traces.Traces {
		if item.TraceID == traceID {
			found = true
			break
		}
	}
	if !found || traces.Count == 0 {
		return fmt.Errorf("trace list did not include %s", traceID)
	}
	var metrics struct {
		TraceCount int `json:"trace_count"`
		Peers      []struct {
			Successes int64 `json:"successes"`
		} `json:"peers"`
	}
	if err := getJSON(client, controller.url+"/metrics", controller.token, &metrics); err != nil {
		return err
	}
	if metrics.TraceCount == 0 || len(metrics.Peers) < 2 {
		return fmt.Errorf("metrics did not report traces and peers: %#v", metrics)
	}
	for _, peer := range metrics.Peers {
		if peer.Successes == 0 {
			return fmt.Errorf("metrics did not report a successful peer call: %#v", metrics)
		}
	}
	return nil
}

func checkRecursive(client *http.Client, controller target) ([]string, error) {
	maxDepth := 2
	request := protocol.RecursiveReplyRequest{
		Reply: protocol.ReplyRequest{
			RequestID: "smoke-recursive",
			Prompt:    "递归读取各节点时间",
			DirectTool: &protocol.ToolInvocation{
				Name: "clock", Arguments: json.RawMessage(`{}`),
			},
		},
		MaxDepth: &maxDepth,
	}
	var response protocol.RecursiveReplyResponse
	if err := postJSON(
		client,
		controller.url+"/v1/recursive/reply",
		controller.token,
		request,
		&response,
	); err != nil {
		return nil, err
	}
	if response.Node != "ecs-controller" || response.Reply == nil ||
		!hasSuccessfulTool(response.Reply.ToolRuns, "clock") ||
		len(response.Children) != 2 {
		return nil, fmt.Errorf("unexpected recursive response: %#v", response)
	}
	nodes := make([]string, 0, len(response.Children))
	for _, child := range response.Children {
		if child.Error != "" || child.Reply == nil ||
			!hasSuccessfulTool(child.Reply.ToolRuns, "clock") {
			return nil, fmt.Errorf("recursive child failed: %#v", child)
		}
		nodes = append(nodes, child.Node)
	}
	sort.Strings(nodes)
	if !sameNodes(nodes) {
		return nil, fmt.Errorf("recursive call did not reach both workers: %#v", nodes)
	}
	return append([]string{"ecs-controller"}, nodes...), nil
}

func postJSON(client *http.Client, url string, token string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s: %s", url, response.Status, data)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func getJSON(client *http.Client, url string, token string, output any) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
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
		return fmt.Errorf("%s returned %s: %s", url, response.Status, data)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func hasSuccessfulTool(runs []protocol.ToolRun, name string) bool {
	for _, run := range runs {
		if run.Name == name && run.Error == "" && json.Valid([]byte(run.Output)) {
			return true
		}
	}
	return false
}

func sameNodes(nodes []string) bool {
	return len(nodes) == 2 && nodes[0] == "ecs-worker-a" && nodes[1] == "ecs-worker-b"
}

func fatal(err error) {
	if err == nil {
		err = errors.New("unknown error")
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
