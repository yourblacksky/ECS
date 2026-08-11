package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"ecs/internal/config"
	"ecs/internal/tools"
)

type Client struct {
	mu   sync.RWMutex
	cfg  config.LLMConfig
	http *http.Client
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Tools           []nativeTool  `json:"tools,omitempty"`
	Temperature     float64       `json:"temperature"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Stream          bool          `json:"stream"`
}

type nativeTool struct {
	Type     string               `json:"type"`
	Function nativeToolDefinition `json:"function"`
}

type nativeToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewClient(cfg config.LLMConfig) *Client {
	return NewClientWithTransport(cfg, nil)
}

func NewClientWithTransport(cfg config.LLMConfig, transport http.RoundTripper) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout:   cfg.Timeout.Duration(),
			Transport: transport,
		},
	}
}

func (c *Client) Complete(ctx context.Context, model string, messages []chatMessage, manifests []tools.Manifest) (chatMessage, error) {
	return c.complete(ctx, model, messages, manifests, 0.1)
}

func (c *Client) CompleteDeterministic(
	ctx context.Context,
	model string,
	messages []chatMessage,
) (chatMessage, error) {
	return c.complete(ctx, model, messages, nil, 0)
}

func (c *Client) complete(
	ctx context.Context,
	model string,
	messages []chatMessage,
	manifests []tools.Manifest,
	temperature float64,
) (chatMessage, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if model == "" {
		model = cfg.Model
	}
	var aliases map[string]string
	var originals map[string]string
	payload := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
	}
	// "none" means disabled in ECS. Omit it because several OpenAI-compatible
	// providers reject the value instead of treating it as no reasoning.
	if cfg.ReasoningEffort != "none" {
		payload.ReasoningEffort = cfg.ReasoningEffort
	}
	if cfg.NativeTools {
		aliases, originals = nativeToolNameAliases(manifests)
		payload.Messages = aliasToolNamesInMessages(messages, aliases)
		for _, manifest := range manifests {
			payload.Tools = append(payload.Tools, nativeTool{
				Type: "function",
				Function: nativeToolDefinition{
					Name:        aliases[manifest.Name],
					Description: manifest.Description,
					Parameters:  manifest.InputSchema,
				},
			})
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return chatMessage{}, err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return chatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return chatMessage{}, fmt.Errorf("LLM request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return chatMessage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return chatMessage{}, fmt.Errorf("LLM returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var result chatResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return chatMessage{}, fmt.Errorf("decode LLM response: %w", err)
	}
	if result.Error != nil {
		return chatMessage{}, errors.New(result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return chatMessage{}, errors.New("LLM returned no choices")
	}
	answer := result.Choices[0].Message
	restoreToolNamesInMessage(&answer, originals)
	return answer, nil
}

// SetAPIKey replaces only the in-memory provider credential. Mobile hosts use
// this after reading their platform keystore; the value is never persisted in
// ECS config or exposed by status endpoints.
func (c *Client) SetAPIKey(value string) {
	c.mu.Lock()
	c.cfg.APIKey = value
	c.mu.Unlock()
}

func (c *Client) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.cfg.APIKey) != ""
}

const maxNativeToolNameLength = 64

// nativeToolNameAliases keeps ECS/Manifest names unchanged inside the runtime
// while exposing provider-compatible function names at the LLM boundary.
func nativeToolNameAliases(manifests []tools.Manifest) (map[string]string, map[string]string) {
	aliases := make(map[string]string, len(manifests))
	originals := make(map[string]string, len(manifests))
	reserved := make(map[string]bool, len(manifests))

	for _, manifest := range manifests {
		if isSafeNativeToolName(manifest.Name) {
			aliases[manifest.Name] = manifest.Name
			originals[manifest.Name] = manifest.Name
			reserved[manifest.Name] = true
		}
	}

	names := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if _, exists := aliases[manifest.Name]; !exists {
			names = append(names, manifest.Name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if _, exists := aliases[name]; exists {
			continue
		}
		alias := safeNativeToolName(name)
		if reserved[alias] {
			digest := sha256.Sum256([]byte(name))
			suffix := fmt.Sprintf("_%x", digest[:4])
			alias = strings.TrimRight(alias[:min(len(alias), maxNativeToolNameLength-len(suffix))], "_") + suffix
		}
		for sequence := 2; reserved[alias]; sequence++ {
			suffix := fmt.Sprintf("_%d", sequence)
			base := safeNativeToolName(name)
			alias = strings.TrimRight(base[:min(len(base), maxNativeToolNameLength-len(suffix))], "_") + suffix
		}
		aliases[name] = alias
		originals[alias] = name
		reserved[alias] = true
	}
	return aliases, originals
}

func isSafeNativeToolName(name string) bool {
	if name == "" || len(name) > maxNativeToolNameLength {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func safeNativeToolName(name string) string {
	var builder strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() == maxNativeToolNameLength {
			break
		}
	}
	alias := strings.Trim(builder.String(), "_")
	if alias == "" {
		return "tool"
	}
	return alias
}

func aliasToolNamesInMessages(messages []chatMessage, aliases map[string]string) []chatMessage {
	aliased := make([]chatMessage, len(messages))
	for index, message := range messages {
		aliased[index] = message
		if message.Role == "tool" {
			if alias, exists := aliases[message.Name]; exists {
				aliased[index].Name = alias
			}
		}
		if len(message.ToolCalls) > 0 {
			aliased[index].ToolCalls = append([]toolCall(nil), message.ToolCalls...)
			for callIndex := range aliased[index].ToolCalls {
				if alias, exists := aliases[aliased[index].ToolCalls[callIndex].Function.Name]; exists {
					aliased[index].ToolCalls[callIndex].Function.Name = alias
				}
			}
		}
	}
	return aliased
}

func restoreToolNamesInMessage(message *chatMessage, originals map[string]string) {
	for index := range message.ToolCalls {
		if original, exists := originals[message.ToolCalls[index].Function.Name]; exists {
			message.ToolCalls[index].Function.Name = original
		}
	}
}
