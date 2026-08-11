// Package schema provides ECS's canonical JSON Schema contract and the
// deterministic boundary between model-facing and runtime-facing arguments.
package schema

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const Draft2020URL = "https://json-schema.org/draft/2020-12/schema"

// Adapter describes deterministic transformations from a compact model input
// into the canonical runtime input. Mappings use canonical.path -> model.path.
type Adapter struct {
	Version  string            `json:"version,omitempty"`
	Mappings map[string]string `json:"mappings,omitempty"`
	Defaults map[string]any    `json:"defaults,omitempty"`
	Context  map[string]string `json:"context,omitempty"`
}

type RuntimeContext struct {
	RequestID string
	TraceID   string
	SessionID string
}

var compiled sync.Map

// ValidateDefinition checks that a schema is a valid, locally self-contained
// JSON Schema Draft 2020-12 document. Remote references are deliberately
// rejected so loading a Manifest never performs network I/O.
func ValidateDefinition(document map[string]any, requireObject bool) error {
	if document == nil {
		return errors.New("schema is required")
	}
	if requireObject && document["type"] != "object" {
		return errors.New("schema root type must be object")
	}
	if err := rejectRemoteReferences(document, "$"); err != nil {
		return err
	}
	_, err := compile(document)
	return err
}

func Validate(raw json.RawMessage, document map[string]any) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("value must be valid JSON")
	}
	validator, err := compile(document)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	if err := validator.Validate(value); err != nil {
		return err
	}
	return nil
}

func ValidateAdapter(adapter Adapter, modelSchema, canonicalSchema map[string]any) error {
	if adapter.Version == "" && len(adapter.Mappings) == 0 && len(adapter.Defaults) == 0 && len(adapter.Context) == 0 {
		return nil
	}
	if adapter.Version != "ecs.adapter/v1" {
		return errors.New("input_adapter.version must be ecs.adapter/v1")
	}
	for target, source := range adapter.Mappings {
		if !validPath(target) || !validPath(source) {
			return fmt.Errorf("invalid adapter mapping %q -> %q", source, target)
		}
	}
	for target := range adapter.Defaults {
		if !validPath(target) {
			return fmt.Errorf("invalid adapter default path %q", target)
		}
	}
	for target, source := range adapter.Context {
		if !validPath(target) {
			return fmt.Errorf("invalid adapter context path %q", target)
		}
		switch source {
		case "request_id", "trace_id", "session_id":
		default:
			return fmt.Errorf("unsupported adapter context source %q", source)
		}
	}
	if modelSchema == nil || canonicalSchema == nil {
		return errors.New("input_adapter requires model and canonical schemas")
	}
	return nil
}

// Adapt validates the compact model object, applies deterministic mappings,
// defaults and trusted runtime context, then validates the canonical object.
func Adapt(
	raw json.RawMessage,
	modelSchema map[string]any,
	canonicalSchema map[string]any,
	adapter Adapter,
	context RuntimeContext,
) (json.RawMessage, []string, error) {
	var model map[string]any
	if err := json.Unmarshal(raw, &model); err != nil {
		return nil, nil, errors.New("arguments must be a JSON object")
	}
	if model == nil {
		return nil, nil, errors.New("arguments must be a JSON object")
	}
	if missing := MissingRequired(model, modelSchema); len(missing) > 0 {
		return nil, missing, nil
	}
	if err := Validate(raw, modelSchema); err != nil {
		return nil, nil, err
	}
	canonical := deepCopyObject(model)
	for target, source := range adapter.Mappings {
		value, ok := getPath(model, source)
		if !ok {
			continue
		}
		deletePath(canonical, source)
		setPath(canonical, target, value)
	}
	for target, value := range adapter.Defaults {
		if _, exists := getPath(canonical, target); !exists {
			setPath(canonical, target, value)
		}
	}
	contextValues := map[string]string{
		"request_id": context.RequestID,
		"trace_id":   context.TraceID,
		"session_id": context.SessionID,
	}
	for target, source := range adapter.Context {
		if value := contextValues[source]; value != "" {
			setPath(canonical, target, value)
		}
	}
	if missing := MissingRequired(canonical, canonicalSchema); len(missing) > 0 {
		return nil, nil, fmt.Errorf(
			"input adapter did not produce required canonical fields: %s",
			strings.Join(missing, ", "),
		)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, nil, err
	}
	if err := Validate(encoded, canonicalSchema); err != nil {
		return nil, nil, err
	}
	return encoded, nil, nil
}

func MissingRequired(value map[string]any, document map[string]any) []string {
	var missing []string
	collectMissing(value, document, "", &missing)
	sort.Strings(missing)
	return missing
}

func collectMissing(value map[string]any, document map[string]any, prefix string, result *[]string) {
	required, _ := document["required"].([]any)
	properties, _ := document["properties"].(map[string]any)
	for _, item := range required {
		name, _ := item.(string)
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		child, exists := value[name]
		if !exists || child == nil {
			*result = append(*result, path)
			continue
		}
		childObject, objectOK := child.(map[string]any)
		childSchema, schemaOK := properties[name].(map[string]any)
		if objectOK && schemaOK {
			collectMissing(childObject, childSchema, path, result)
		}
	}
}

func compile(document map[string]any) (*jsonschema.Schema, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if cached, ok := compiled.Load(key); ok {
		return cached.(*jsonschema.Schema), nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	location := "urn:ecs:schema:" + key
	if err := compiler.AddResource(location, decoded); err != nil {
		return nil, err
	}
	validator, err := compiler.Compile(location)
	if err != nil {
		return nil, err
	}
	actual, _ := compiled.LoadOrStore(key, validator)
	return actual.(*jsonschema.Schema), nil
}

func rejectRemoteReferences(value any, path string) error {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "$ref" {
				reference, ok := child.(string)
				if !ok || !strings.HasPrefix(reference, "#") {
					return fmt.Errorf("%s.$ref must be a local fragment", path)
				}
			}
			if err := rejectRemoteReferences(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range current {
			if err := rejectRemoteReferences(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validPath(path string) bool {
	if strings.TrimSpace(path) != path || path == "" {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			return false
		}
	}
	return true
}

func deepCopyObject(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var copied map[string]any
	_ = json.Unmarshal(encoded, &copied)
	return copied
}

func getPath(object map[string]any, path string) (any, bool) {
	var current any = object
	for _, segment := range strings.Split(path, ".") {
		next, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = next[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setPath(object map[string]any, path string, value any) {
	segments := strings.Split(path, ".")
	current := object
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

func deletePath(object map[string]any, path string) {
	segments := strings.Split(path, ".")
	current := object
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, segments[len(segments)-1])
}
