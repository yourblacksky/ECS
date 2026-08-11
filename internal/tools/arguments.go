package tools

import (
	"encoding/json"

	ecsschema "ecs/internal/schema"
)

// PrepareArguments converts model-facing arguments into the canonical runtime
// contract. A non-empty missing result means execution must pause for slot
// completion instead of guessing values.
func PrepareArguments(
	raw json.RawMessage,
	manifest Manifest,
	context ecsschema.RuntimeContext,
) (json.RawMessage, []string, error) {
	canonical, missing, err := ecsschema.Adapt(
		raw,
		manifest.InputSchema,
		manifest.RuntimeInputSchema(),
		manifest.InputAdapter,
		context,
	)
	if err != nil {
		return nil, nil, &ArgumentValidationError{Err: err}
	}
	return canonical, missing, nil
}
