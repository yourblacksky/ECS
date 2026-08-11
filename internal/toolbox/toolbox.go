package toolbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func BuiltinNames() []string {
	names := make([]string, 0, len(builtinHandlers))
	for name := range builtinHandlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Handler(name string) func(context.Context, json.RawMessage) (string, error) {
	handler, ok := builtinHandlers[name]
	if !ok {
		return nil
	}
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var output bytes.Buffer
		if err := handler(bytes.NewReader(input), &output); err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return output.String(), err
		}
		return strings.TrimSpace(output.String()), nil
	}
}

func Run(name string, input io.Reader, output io.Writer, errorOutput io.Writer) int {
	err := execute(name, input, output)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return 1
	}
	return 0
}

func execute(name string, input io.Reader, output io.Writer) error {
	handler, ok := builtinHandlers[name]
	if !ok {
		return fmt.Errorf("unknown built-in tool %q", name)
	}
	return handler(input, output)
}

type builtinHandler func(io.Reader, io.Writer) error

// builtinHandlers is the single source of truth for names and implementations.
// A built-in can no longer be advertised without also providing executable code.
var builtinHandlers = map[string]builtinHandler{
	"calculate":                calculate,
	"clock":                    func(_ io.Reader, output io.Writer) error { return clock(output) },
	"execution_receipt_verify": executionReceiptVerify,
	"make_id":                  makeID,
	"meeting_evidence_verify":  meetingEvidenceVerify,
	"meeting_fact_extract":     meetingFactExtract,
	"meeting_scope_plan":       meetingScopePlan,
	"office_action_compensate": officeActionCompensate,
	"office_action_dispatch":   officeActionDispatch,
	"text_count_cn":            textCountCN,
	"text_diff":                textDiff,
	"text_extract":             textExtract,
	"text_replace":             textReplace,
	"text_stats":               textStats,
	"workdays":                 workdays,
}

func clock(output io.Writer) error {
	now := time.Now()
	return writeJSON(output, map[string]any{
		"time":     now.Format(time.RFC3339),
		"timezone": now.Location().String(),
	})
}

func calculate(input io.Reader, output io.Writer) error {
	var arguments struct {
		A  float64 `json:"a"`
		Op string  `json:"op"`
		B  float64 `json:"b"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	var result float64
	switch arguments.Op {
	case "add":
		result = arguments.A + arguments.B
	case "subtract":
		result = arguments.A - arguments.B
	case "multiply":
		result = arguments.A * arguments.B
	case "divide":
		if arguments.B == 0 {
			return errors.New("cannot divide by zero")
		}
		result = arguments.A / arguments.B
	case "power":
		result = math.Pow(arguments.A, arguments.B)
	case "log_base":
		if arguments.A <= 0 {
			return errors.New("logarithm value must be greater than zero")
		}
		if arguments.B <= 0 || arguments.B == 1 {
			return errors.New("logarithm base must be greater than zero and not equal to one")
		}
		result = math.Log(arguments.A) / math.Log(arguments.B)
		if nearest := math.Round(result); math.Abs(result-nearest) < 1e-12 {
			result = nearest
		}
	default:
		return errors.New("op must be add, subtract, multiply, divide, power, or log_base")
	}
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return errors.New("calculation result is not a finite number")
	}
	return writeJSON(output, map[string]any{"result": result})
}

func textStats(input io.Reader, output io.Writer) error {
	var arguments struct {
		Text string `json:"text"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	lines := 0
	if arguments.Text != "" {
		lines = strings.Count(arguments.Text, "\n") + 1
	}
	return writeJSON(output, map[string]any{
		"characters": utf8.RuneCountInString(arguments.Text),
		"words":      len(strings.Fields(arguments.Text)),
		"lines":      lines,
	})
}

func textReplace(input io.Reader, output io.Writer) error {
	var arguments struct {
		Text string `json:"text"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	if arguments.Old == "" {
		return errors.New("old cannot be empty")
	}
	return writeJSON(output, map[string]any{
		"text": strings.ReplaceAll(arguments.Text, arguments.Old, arguments.New),
	})
}

func makeID(input io.Reader, output io.Writer) error {
	var arguments struct {
		Prefix string `json:"prefix"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return err
	}
	return writeJSON(output, map[string]any{
		"id": arguments.Prefix + hex.EncodeToString(value[:]),
	})
}

func decode(input io.Reader, value any) error {
	decoder := json.NewDecoder(io.LimitReader(input, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	return nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func decimal(value int) string {
	return strconv.Itoa(value)
}
