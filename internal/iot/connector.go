package iot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"ecs/internal/config"
	"ecs/internal/platform"
)

const defaultMQTTWait = 5 * time.Second

// Connector executes administrator-configured IoT profiles. Model-visible
// arguments never contain an arbitrary destination host or broker.
type Connector struct {
	cfg       config.IoTConfig
	transport http.RoundTripper
}

func New(cfg config.IoTConfig, transport http.RoundTripper) *Connector {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Connector{cfg: cfg, transport: transport}
}

func (c *Connector) HTTPAvailability() platform.ToolAvailability {
	return profileAvailability(len(c.cfg.HTTP), c.readyHTTPProfiles())
}

func (c *Connector) MQTTAvailability() platform.ToolAvailability {
	return profileAvailability(len(c.cfg.MQTT), c.readyMQTTProfiles())
}

func profileAvailability(configured int, ready []string) platform.ToolAvailability {
	if len(ready) > 0 {
		return platform.ToolAvailability{Available: true}
	}
	if configured == 0 {
		return platform.ToolAvailability{Reason: "connector_unconfigured"}
	}
	return platform.ToolAvailability{Reason: "connector_credentials_unavailable"}
}

func (c *Connector) readyHTTPProfiles() []string {
	ready := make([]string, 0, len(c.cfg.HTTP))
	for name, profile := range c.cfg.HTTP {
		if headerEnvironmentReady(profile.HeadersEnv) {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	return ready
}

func (c *Connector) readyMQTTProfiles() []string {
	ready := make([]string, 0, len(c.cfg.MQTT))
	for name, profile := range c.cfg.MQTT {
		if environmentReady(profile.UsernameEnv) && environmentReady(profile.PasswordEnv) {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	return ready
}

func headerEnvironmentReady(headers map[string]string) bool {
	for _, environment := range headers {
		if os.Getenv(environment) == "" {
			return false
		}
	}
	return true
}

func environmentReady(name string) bool {
	return name == "" || os.Getenv(name) != ""
}

type httpArguments struct {
	Profile string            `json:"profile"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

func (c *Connector) HTTP(ctx context.Context, raw json.RawMessage) (string, error) {
	var arguments httpArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return "", fmt.Errorf("decode HTTP connector arguments: %w", err)
	}
	profile, ok := c.cfg.HTTP[arguments.Profile]
	if !ok {
		return "", fmt.Errorf("HTTP IoT profile %q is not configured", arguments.Profile)
	}
	if !headerEnvironmentReady(profile.HeadersEnv) {
		return "", fmt.Errorf("HTTP IoT profile %q credentials are unavailable", arguments.Profile)
	}
	method := strings.ToUpper(strings.TrimSpace(arguments.Method))
	if !containsFold(profile.AllowedMethods, method) {
		return "", fmt.Errorf("HTTP method %s is not allowed by profile %q", method, arguments.Profile)
	}
	cleanPath, err := allowedHTTPPath(arguments.Path, profile.AllowedPathPrefixes)
	if err != nil {
		return "", err
	}
	destination, err := profileURL(profile.BaseURL, cleanPath, arguments.Query)
	if err != nil {
		return "", err
	}
	var body io.Reader
	if len(arguments.Body) > 0 && string(arguments.Body) != "null" {
		body = bytes.NewReader(arguments.Body)
	}
	request, err := http.NewRequestWithContext(ctx, method, destination, body)
	if err != nil {
		return "", fmt.Errorf("create HTTP IoT request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for header, environment := range profile.HeadersEnv {
		request.Header.Set(header, os.Getenv(environment))
	}
	client := &http.Client{
		Transport: c.transport,
		Timeout:   profile.Timeout.Duration(),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("HTTP IoT request failed: %w", err)
	}
	defer response.Body.Close()
	limit := c.cfg.MaxResponseBytes
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("read HTTP IoT response: %w", err)
	}
	if int64(len(payload)) > limit {
		return "", fmt.Errorf("HTTP IoT response exceeds %d bytes", limit)
	}
	result, err := encodeResult(map[string]any{
		"profile":      arguments.Profile,
		"status_code":  response.StatusCode,
		"content_type": response.Header.Get("Content-Type"),
		"body":         decodePayload(payload),
		"duration_ms":  time.Since(started).Milliseconds(),
	})
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("HTTP IoT endpoint returned status %d", response.StatusCode)
	}
	return result, nil
}

func allowedHTTPPath(value string, prefixes []string) (string, error) {
	if !strings.HasPrefix(value, "/") {
		return "", errors.New("HTTP IoT path must start with /")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("HTTP IoT path must not contain a query or fragment")
	}
	decoded, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", errors.New("HTTP IoT path contains invalid escaping")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("HTTP IoT path traversal is not allowed")
		}
	}
	cleaned := path.Clean(decoded)
	for _, prefix := range prefixes {
		normalizedPrefix := path.Clean(prefix)
		if normalizedPrefix == "/" || cleaned == normalizedPrefix ||
			strings.HasPrefix(cleaned, normalizedPrefix+"/") {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("HTTP IoT path %q is outside the configured prefixes", value)
}

func profileURL(baseURL, requestPath string, query map[string]string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse HTTP IoT profile URL: %w", err)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + requestPath
	values := parsed.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

type mqttArguments struct {
	Profile   string          `json:"profile"`
	Operation string          `json:"operation"`
	Topic     string          `json:"topic"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	QoS       int             `json:"qos,omitempty"`
	Retain    bool            `json:"retain,omitempty"`
	WaitMS    int             `json:"wait_ms,omitempty"`
}

func (c *Connector) MQTT(ctx context.Context, raw json.RawMessage) (string, error) {
	var arguments mqttArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return "", fmt.Errorf("decode MQTT connector arguments: %w", err)
	}
	profile, ok := c.cfg.MQTT[arguments.Profile]
	if !ok {
		return "", fmt.Errorf("MQTT IoT profile %q is not configured", arguments.Profile)
	}
	if !environmentReady(profile.UsernameEnv) || !environmentReady(profile.PasswordEnv) {
		return "", fmt.Errorf("MQTT IoT profile %q credentials are unavailable", arguments.Profile)
	}
	if !allowedTopic(arguments.Topic, profile.AllowedTopicPrefixes) {
		return "", fmt.Errorf("MQTT topic %q is outside the configured prefixes", arguments.Topic)
	}
	if arguments.QoS < 0 || arguments.QoS > 1 {
		return "", errors.New("MQTT QoS must be 0 or 1")
	}
	if (arguments.WaitMS > 0 && arguments.WaitMS < 100) || arguments.WaitMS > 30000 {
		return "", errors.New("MQTT wait_ms must be between 100 and 30000 when provided")
	}
	if arguments.Operation == "publish" && strings.ContainsAny(arguments.Topic, "+#") {
		return "", errors.New("MQTT publish topic cannot contain wildcards")
	}
	options := mqtt.NewClientOptions().
		AddBroker(normalizeBrokerURL(profile.BrokerURL)).
		SetClientID(mqttClientID(profile.ClientID)).
		SetConnectTimeout(profile.Timeout.Duration()).
		SetAutoReconnect(false).
		SetCleanSession(true)
	if profile.UsernameEnv != "" {
		options.SetUsername(os.Getenv(profile.UsernameEnv))
	}
	if profile.PasswordEnv != "" {
		options.SetPassword(os.Getenv(profile.PasswordEnv))
	}
	client := mqtt.NewClient(options)
	if err := waitToken(ctx, client.Connect()); err != nil {
		return "", fmt.Errorf("connect MQTT IoT broker: %w", err)
	}
	defer client.Disconnect(250)

	switch arguments.Operation {
	case "publish":
		if len(arguments.Payload) == 0 {
			return "", errors.New("MQTT publish requires payload")
		}
		payload := mqttPayload(arguments.Payload)
		if int64(len(payload)) > c.cfg.MaxResponseBytes {
			return "", fmt.Errorf("MQTT publish payload exceeds %d bytes", c.cfg.MaxResponseBytes)
		}
		if err := waitToken(ctx, client.Publish(
			arguments.Topic, byte(arguments.QoS), arguments.Retain, payload,
		)); err != nil {
			return "", fmt.Errorf("publish MQTT IoT message: %w", err)
		}
		return encodeResult(map[string]any{
			"profile": arguments.Profile, "operation": "publish", "topic": arguments.Topic,
			"qos": arguments.QoS, "retained": arguments.Retain, "delivered": true,
		})
	case "subscribe":
		return c.subscribe(ctx, client, arguments)
	default:
		return "", errors.New("MQTT operation must be publish or subscribe")
	}
}

func (c *Connector) subscribe(
	ctx context.Context,
	client mqtt.Client,
	arguments mqttArguments,
) (string, error) {
	type message struct {
		topic    string
		payload  []byte
		tooLarge bool
	}
	messages := make(chan message, 1)
	token := client.Subscribe(arguments.Topic, byte(arguments.QoS), func(_ mqtt.Client, value mqtt.Message) {
		select {
		case messages <- message{
			topic: value.Topic(), payload: append([]byte(nil), value.Payload()...),
			tooLarge: int64(len(value.Payload())) > c.cfg.MaxResponseBytes,
		}:
		default:
		}
	})
	if err := waitToken(ctx, token); err != nil {
		return "", fmt.Errorf("subscribe MQTT IoT topic: %w", err)
	}
	defer client.Unsubscribe(arguments.Topic)
	wait := defaultMQTTWait
	if arguments.WaitMS > 0 {
		wait = time.Duration(arguments.WaitMS) * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("MQTT IoT subscription timed out after %s", wait)
	case received := <-messages:
		if received.tooLarge {
			return "", fmt.Errorf("MQTT subscription payload exceeds %d bytes", c.cfg.MaxResponseBytes)
		}
		return encodeResult(map[string]any{
			"profile": arguments.Profile, "operation": "subscribe", "topic": received.topic,
			"qos": arguments.QoS, "payload": decodePayload(received.payload),
		})
	}
}

func waitToken(ctx context.Context, token mqtt.Token) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}

func allowedTopic(topic string, prefixes []string) bool {
	if strings.TrimSpace(topic) == "" || strings.ContainsRune(topic, '\x00') {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(topic, prefix) {
			return true
		}
	}
	return false
}

func normalizeBrokerURL(value string) string {
	switch {
	case strings.HasPrefix(value, "mqtt://"):
		return "tcp://" + strings.TrimPrefix(value, "mqtt://")
	case strings.HasPrefix(value, "mqtts://"):
		return "ssl://" + strings.TrimPrefix(value, "mqtts://")
	default:
		return value
	}
}

func mqttClientID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	if strings.TrimSpace(prefix) == "" {
		prefix = "ecs-iot"
	}
	return strings.TrimRight(prefix, "-") + "-" + hex.EncodeToString(random[:])
}

func mqttPayload(raw json.RawMessage) []byte {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []byte(text)
	}
	return append([]byte(nil), raw...)
}

func decodePayload(payload []byte) any {
	if len(payload) == 0 {
		return ""
	}
	var decoded any
	if json.Unmarshal(payload, &decoded) == nil {
		return decoded
	}
	return string(payload)
}

func encodeResult(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode IoT connector result: %w", err)
	}
	return string(encoded), nil
}

func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), candidate) {
			return true
		}
	}
	return false
}
