package iot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"ecs/internal/config"
)

func TestHTTPUsesConfiguredProfileAndEnvironmentHeader(t *testing.T) {
	t.Setenv("ECS_TEST_IOT_TOKEN", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/device/status" || request.URL.Query().Get("detail") != "full" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		if request.Header.Get("Authorization") != "secret-token" {
			t.Fatalf("missing environment-backed header: %#v", request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"online":true}`))
	}))
	defer server.Close()

	connector := New(config.IoTConfig{
		MaxResponseBytes: 4096,
		HTTP: map[string]config.IoTHTTPProfile{
			"lab": {
				BaseURL: server.URL, AllowedMethods: []string{"GET"},
				AllowedPathPrefixes: []string{"/device/"},
				HeadersEnv:          map[string]string{"Authorization": "ECS_TEST_IOT_TOKEN"},
				Timeout:             config.Duration(time.Second),
			},
		},
	}, http.DefaultTransport)
	if !connector.HTTPAvailability().Available {
		t.Fatal("configured HTTP connector should be available")
	}
	result, err := connector.HTTP(context.Background(), json.RawMessage(
		`{"profile":"lab","method":"GET","path":"/device/status","query":{"detail":"full"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"online":true`) || !strings.Contains(result, `"status_code":200`) {
		t.Fatalf("unexpected HTTP connector result: %s", result)
	}
}

func TestHTTPRejectsUnknownProfileAndPathTraversal(t *testing.T) {
	connector := New(config.IoTConfig{
		MaxResponseBytes: 4096,
		HTTP: map[string]config.IoTHTTPProfile{
			"lab": {
				BaseURL: "http://127.0.0.1:1", AllowedMethods: []string{"GET"},
				AllowedPathPrefixes: []string{"/device/"}, Timeout: config.Duration(time.Second),
			},
		},
	}, nil)
	if _, err := connector.HTTP(context.Background(), json.RawMessage(
		`{"profile":"other","method":"GET","path":"/device/status"}`,
	)); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown profile was not rejected: %v", err)
	}
	if _, err := connector.HTTP(context.Background(), json.RawMessage(
		`{"profile":"lab","method":"GET","path":"/device/../admin"}`,
	)); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("path traversal was not rejected: %v", err)
	}
}

func TestConnectorAvailabilityRequiresProfilesAndCredentials(t *testing.T) {
	connector := New(config.IoTConfig{}, nil)
	if got := connector.HTTPAvailability(); got.Available || got.Reason != "connector_unconfigured" {
		t.Fatalf("unexpected empty HTTP availability: %#v", got)
	}
	t.Setenv("ECS_TEST_MQTT_PASSWORD", "")
	connector = New(config.IoTConfig{MQTT: map[string]config.IoTMQTTProfile{
		"lab": {PasswordEnv: "ECS_TEST_MQTT_PASSWORD"},
	}}, nil)
	if got := connector.MQTTAvailability(); got.Available || got.Reason != "connector_credentials_unavailable" {
		t.Fatalf("unexpected credential availability: %#v", got)
	}
}

func TestMQTTBoundariesAndPayload(t *testing.T) {
	if !allowedTopic("ecs/device/one/set", []string{"ecs/device/"}) {
		t.Fatal("allowed MQTT topic was rejected")
	}
	if allowedTopic("other/device/set", []string{"ecs/device/"}) {
		t.Fatal("outside MQTT topic was allowed")
	}
	if got := normalizeBrokerURL("mqtts://broker.example:8883"); got != "ssl://broker.example:8883" {
		t.Fatalf("unexpected normalized broker: %s", got)
	}
	if got := string(mqttPayload(json.RawMessage(`"on"`))); got != "on" {
		t.Fatalf("string payload should be published without JSON quotes: %q", got)
	}
}

func TestMQTTIntegration(t *testing.T) {
	broker := os.Getenv("ECS_TEST_MQTT_BROKER")
	if broker == "" {
		t.Skip("ECS_TEST_MQTT_BROKER is not set")
	}
	topic := fmt.Sprintf("ecs/device/test-%d/state", time.Now().UnixNano())
	profile := config.IoTMQTTProfile{
		BrokerURL: broker, AllowedTopicPrefixes: []string{"ecs/device/"},
		Timeout: config.Duration(5 * time.Second),
	}
	connector := New(config.IoTConfig{
		MaxResponseBytes: 4096,
		MQTT:             map[string]config.IoTMQTTProfile{"test": profile},
	}, nil)

	observer := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker(normalizeBrokerURL(broker)).
		SetClientID(mqttClientID("ecs-iot-test-observer")))
	if err := waitToken(context.Background(), observer.Connect()); err != nil {
		t.Fatal(err)
	}
	defer observer.Disconnect(100)
	received := make(chan string, 1)
	if err := waitToken(context.Background(), observer.Subscribe(topic, 1, func(_ mqtt.Client, message mqtt.Message) {
		received <- string(message.Payload())
	})); err != nil {
		t.Fatal(err)
	}

	result, err := connector.MQTT(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"profile":"test","operation":"publish","topic":%q,"payload":{"value":42},"qos":1}`,
		topic,
	)))
	if err != nil || !strings.Contains(result, `"delivered":true`) {
		t.Fatalf("MQTT publish failed: result=%s err=%v", result, err)
	}
	select {
	case payload := <-received:
		if payload != `{"value":42}` {
			t.Fatalf("unexpected MQTT payload: %s", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("published MQTT message was not observed")
	}

	readResult := make(chan string, 1)
	readError := make(chan error, 1)
	go func() {
		output, readErr := connector.MQTT(context.Background(), json.RawMessage(fmt.Sprintf(
			`{"profile":"test","operation":"subscribe","topic":%q,"qos":1,"wait_ms":5000}`,
			topic,
		)))
		readResult <- output
		readError <- readErr
	}()
	time.Sleep(250 * time.Millisecond)
	if err := waitToken(context.Background(), observer.Publish(topic, 1, false, `{"online":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := <-readError; err != nil {
		t.Fatal(err)
	}
	if output := <-readResult; !strings.Contains(output, `"online":true`) {
		t.Fatalf("unexpected MQTT subscribe output: %s", output)
	}
}
