package browsernode

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"

	"github.com/gorilla/websocket"
)

func TestHubEnrollsAuthenticatesAndDispatchesTask(t *testing.T) {
	manager, err := networkauth.New(config.NetworkAuthConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		LeaseTimeout:  config.Duration(45 * time.Second),
		EnrollmentTTL: config.Duration(time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	hub := New(manager, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(hub)
	defer server.Close()
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http")

	privateKey, nodeID, jwk := browserIdentity(t)
	first, enrollment := connectBrowser(t, websocketURL, privateKey, nodeID, jwk)
	if enrollment.Type != "enrollment" || enrollment.Status != networkauth.StatusPending {
		t.Fatalf("unexpected enrollment response: %#v", enrollment)
	}
	_ = first.Close()
	if _, err := manager.Approve(enrollment.EnrollmentID, networkauth.Approval{}); err != nil {
		t.Fatal(err)
	}

	second, authenticated := connectBrowser(t, websocketURL, privateKey, nodeID, jwk)
	if authenticated.Type != "authenticated" || authenticated.Status != networkauth.StatusActive {
		t.Fatalf("unexpected authentication response: %#v", authenticated)
	}
	firstTaskDone := make(chan struct{})
	go func() {
		defer close(firstTaskDone)
		var task wireMessage
		if err := second.ReadJSON(&task); err != nil {
			return
		}
		_ = second.WriteJSON(wireMessage{Type: "task_ack", TaskID: task.TaskID})
		_ = second.WriteJSON(wireMessage{
			Type:   "task_result",
			TaskID: task.TaskID,
			Result: json.RawMessage(`{"result":5}`),
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := hub.Dispatch(ctx, nodeID, TaskRequest{
		Tool:      "calculate",
		Arguments: json.RawMessage(`{"a":2,"op":"add","b":3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Result) != `{"result":5}` {
		t.Fatalf("task result = %s", result.Result)
	}
	<-firstTaskDone
	cancelSeen := make(chan wireMessage, 1)
	ackSeen := make(chan struct{})
	go func() {
		var task wireMessage
		if err := second.ReadJSON(&task); err != nil {
			return
		}
		_ = second.WriteJSON(wireMessage{Type: "task_ack", TaskID: task.TaskID})
		close(ackSeen)
		var canceled wireMessage
		if err := second.ReadJSON(&canceled); err == nil {
			cancelSeen <- canceled
		}
	}()
	cancelContext, cancelTask := context.WithCancel(context.Background())
	dispatchDone := make(chan error, 1)
	go func() {
		_, dispatchErr := hub.Dispatch(cancelContext, nodeID, TaskRequest{
			Tool:      "calculate",
			Arguments: json.RawMessage(`{"a":2,"op":"add","b":3}`),
		})
		dispatchDone <- dispatchErr
	}()
	select {
	case <-ackSeen:
	case <-time.After(time.Second):
		t.Fatal("browser did not acknowledge cancellable task")
	}
	cancelTask()
	err = <-dispatchDone
	if !errors.Is(err, context.DeadlineExceeded) {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled task error = %v", err)
		}
	}
	select {
	case canceled := <-cancelSeen:
		if canceled.Type != "task_cancel" || canceled.TaskID == "" {
			t.Fatalf("unexpected cancellation message: %#v", canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("browser task cancellation was not sent")
	}
	_ = second.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if relationships := manager.List(); len(relationships) == 1 &&
			relationships[0].Status == networkauth.StatusOffline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("browser connection did not close before test cleanup")
}

func connectBrowser(
	t *testing.T,
	endpoint string,
	privateKey *ecdsa.PrivateKey,
	nodeID string,
	jwk json.RawMessage,
) (*websocket.Conn, wireMessage) {
	t.Helper()
	socket, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	var challenge wireMessage
	if err := socket.ReadJSON(&challenge); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(challengeMessage(challenge.Challenge, nodeID)))
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(paddedInteger(r), paddedInteger(s)...)
	if err := socket.WriteJSON(wireMessage{
		Type:        "authenticate",
		NodeID:      nodeID,
		NodeName:    "Browser test",
		PairingCode: "123456",
		PublicKey:   jwk,
		Signature:   base64.RawURLEncoding.EncodeToString(signature),
		Capabilities: protocol.NodeCapabilities{
			Protocol: "ecs/1",
			NodeID:   nodeID,
			Node:     "Browser test",
			URL:      "browser://" + nodeID,
			Platform: protocol.PlatformCapabilities{
				Name:         "browser",
				RuntimeMode:  "client",
				TopologyRole: "leaf",
			},
			Tools: []protocol.ToolCapability{{
				Name: "calculate", Version: "1.0.0", Description: "calculate", Risk: "low",
				InputSchema: map[string]any{"type": "object"},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var response wireMessage
	if err := socket.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	return socket, response
}

func browserIdentity(t *testing.T) (*ecdsa.PrivateKey, string, json.RawMessage) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rawKey := elliptic.Marshal(elliptic.P256(), privateKey.X, privateKey.Y)
	fingerprint := sha256.Sum256(rawKey)
	nodeID := "ecs_" + hex.EncodeToString(fingerprint[:16])
	jwk, err := json.Marshal(publicJWK{
		KTY: "EC",
		CRV: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y:   base64.RawURLEncoding.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	})
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, nodeID, jwk
}
