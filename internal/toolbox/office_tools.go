package toolbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
)

type meetingActionItem struct {
	Summary  string `json:"summary"`
	Owner    string `json:"owner,omitempty"`
	Deadline string `json:"deadline,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type actionReceipt struct {
	ReceiptID         string `json:"receipt_id"`
	Action            string `json:"action"`
	Target            string `json:"target"`
	Result            string `json:"result"`
	ExternalReference string `json:"external_reference"`
	IdempotencyKey    string `json:"idempotency_key"`
	CreatedAt         string `json:"created_at"`
}

func meetingScopePlan(input io.Reader, output io.Writer) error {
	var arguments struct {
		MeetingTitle string   `json:"meeting_title"`
		Participants []string `json:"participants"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	title := strings.TrimSpace(arguments.MeetingTitle)
	if title == "" {
		return errors.New("meeting_title cannot be empty")
	}
	participants := make([]string, 0, len(arguments.Participants))
	seen := make(map[string]bool, len(arguments.Participants))
	for _, participant := range arguments.Participants {
		participant = strings.TrimSpace(participant)
		if participant != "" && !seen[participant] {
			seen[participant] = true
			participants = append(participants, participant)
		}
	}
	meetingType := "meeting"
	for _, candidate := range []struct{ keyword, kind string }{
		{"周会", "weekly"}, {"评审", "review"}, {"复盘", "retrospective"}, {"交付", "delivery"},
	} {
		if strings.Contains(title, candidate.keyword) {
			meetingType = candidate.kind
			break
		}
	}
	return writeJSON(output, map[string]any{
		"meeting_title": title,
		"meeting_type":  meetingType,
		"participants":  participants,
		"output_scope":  []string{"decisions", "action_items", "unresolved"},
	})
}

func meetingFactExtract(input io.Reader, output io.Writer) error {
	var arguments struct {
		Source string `json:"source"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	source := strings.TrimSpace(arguments.Source)
	if source == "" {
		return errors.New("source cannot be empty")
	}
	decisions := make([]string, 0)
	actions := make([]meetingActionItem, 0)
	unresolved := make([]string, 0)
	for _, rawLine := range strings.Split(source, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case hasAnyPrefix(line, "决定：", "决议：", "decision:", "Decision:"):
			decisions = append(decisions, valueAfterPrefix(line))
		case hasAnyPrefix(line, "行动：", "待办：", "action:", "Action:"):
			actions = append(actions, parseMeetingAction(valueAfterPrefix(line), line))
		case hasAnyPrefix(line, "待确认：", "未决：", "unresolved:", "Unresolved:"):
			unresolved = append(unresolved, valueAfterPrefix(line))
		}
	}
	sum := sha256.Sum256([]byte(source))
	return writeJSON(output, map[string]any{
		"decisions":     decisions,
		"action_items":  actions,
		"unresolved":    unresolved,
		"source_sha256": hex.EncodeToString(sum[:]),
	})
}

func meetingEvidenceVerify(input io.Reader, output io.Writer) error {
	var arguments struct {
		ActionItems []meetingActionItem `json:"action_items"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	blockers := make([]map[string]any, 0)
	for index, item := range arguments.ActionItems {
		var missing []string
		if strings.TrimSpace(item.Summary) == "" {
			missing = append(missing, "summary")
		}
		if strings.TrimSpace(item.Owner) == "" {
			missing = append(missing, "owner")
		}
		if strings.TrimSpace(item.Deadline) == "" {
			missing = append(missing, "deadline")
		}
		if strings.TrimSpace(item.Evidence) == "" {
			missing = append(missing, "evidence")
		}
		if len(missing) > 0 {
			blockers = append(blockers, map[string]any{"index": index, "missing": missing})
		}
	}
	return writeJSON(output, map[string]any{
		"valid":          len(blockers) == 0 && len(arguments.ActionItems) > 0,
		"action_count":   len(arguments.ActionItems),
		"blockers":       blockers,
		"verified_items": arguments.ActionItems,
	})
}

func officeActionDispatch(input io.Reader, output io.Writer) error {
	var arguments struct {
		ActionItems    []meetingActionItem `json:"action_items"`
		IdempotencyKey string              `json:"idempotency_key"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	if len(arguments.ActionItems) == 0 {
		return errors.New("action_items cannot be empty")
	}
	if strings.TrimSpace(arguments.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	receipts := make([]actionReceipt, 0, len(arguments.ActionItems))
	for index, item := range arguments.ActionItems {
		if strings.TrimSpace(item.Summary) == "" || strings.TrimSpace(item.Owner) == "" {
			return errors.New("each action item requires summary and owner")
		}
		key := arguments.IdempotencyKey + ":" + decimal(index)
		sum := sha256.Sum256([]byte(key))
		receiptID := "local_" + hex.EncodeToString(sum[:12])
		receipts = append(receipts, actionReceipt{
			ReceiptID: receiptID, Action: item.Summary, Target: item.Owner,
			Result: "created", ExternalReference: "local-task://" + receiptID,
			IdempotencyKey: key, CreatedAt: now,
		})
	}
	return writeJSON(output, map[string]any{"receipts": receipts})
}

func officeActionCompensate(input io.Reader, output io.Writer) error {
	var arguments struct {
		Action         string          `json:"action"`
		WorkOrder      string          `json:"work_order"`
		TaskID         string          `json:"task_id"`
		IdempotencyKey string          `json:"idempotency_key"`
		Receipts       []actionReceipt `json:"receipts,omitempty"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	if strings.TrimSpace(arguments.Action) == "" ||
		strings.TrimSpace(arguments.WorkOrder) == "" ||
		strings.TrimSpace(arguments.TaskID) == "" ||
		strings.TrimSpace(arguments.IdempotencyKey) == "" {
		return errors.New("action, work_order, task_id and idempotency_key are required")
	}
	sum := sha256.Sum256([]byte(arguments.IdempotencyKey))
	receiptID := "comp_" + hex.EncodeToString(sum[:12])
	return writeJSON(output, map[string]any{
		"receipts": []actionReceipt{{
			ReceiptID: receiptID, Action: arguments.Action, Target: arguments.TaskID,
			Result: "compensated", ExternalReference: "local-compensation://" + receiptID,
			IdempotencyKey: arguments.IdempotencyKey,
			CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		}},
	})
}

func executionReceiptVerify(input io.Reader, output io.Writer) error {
	var arguments struct {
		Receipts []actionReceipt `json:"receipts"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	invalid := make([]int, 0)
	for index, receipt := range arguments.Receipts {
		if receipt.ReceiptID == "" || receipt.Action == "" || receipt.Target == "" ||
			receipt.Result != "created" || receipt.ExternalReference == "" ||
			receipt.IdempotencyKey == "" || receipt.CreatedAt == "" {
			invalid = append(invalid, index)
		}
	}
	return writeJSON(output, map[string]any{
		"valid": len(arguments.Receipts) > 0 && len(invalid) == 0,
		"count": len(arguments.Receipts), "invalid_receipts": invalid,
	})
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func valueAfterPrefix(value string) string {
	if _, remainder, found := strings.Cut(value, "："); found {
		return strings.TrimSpace(remainder)
	}
	if _, remainder, found := strings.Cut(value, ":"); found {
		return strings.TrimSpace(remainder)
	}
	return strings.TrimSpace(value)
}

func parseMeetingAction(value string, evidence string) meetingActionItem {
	item := meetingActionItem{Evidence: evidence}
	for index, part := range strings.Split(value, "|") {
		part = strings.TrimSpace(part)
		key, field, found := strings.Cut(part, "=")
		if !found {
			if index == 0 {
				item.Summary = part
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "owner", "责任人":
			item.Owner = strings.TrimSpace(field)
		case "due", "deadline", "截止":
			item.Deadline = strings.TrimSpace(field)
		case "evidence", "依据":
			item.Evidence = strings.TrimSpace(field)
		}
	}
	return item
}
