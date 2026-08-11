package toolbox

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func textCountCN(input io.Reader, output io.Writer) error {
	var arguments struct {
		Text string `json:"text"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	var chinese, letters, digits, punctuation, spaces int
	for _, character := range arguments.Text {
		switch {
		case character >= '\u4e00' && character <= '\u9fff':
			chinese++
		case character >= '\u3000' && character <= '\u303f',
			character >= '\uff00' && character <= '\uffef':
			if unicode.IsDigit(character) {
				digits++
			} else {
				punctuation++
			}
		case character <= unicode.MaxASCII && unicode.IsLetter(character):
			letters++
		case character <= unicode.MaxASCII && unicode.IsDigit(character):
			digits++
		case unicode.IsSpace(character):
			spaces++
		default:
			punctuation++
		}
	}
	total := utf8.RuneCountInString(arguments.Text)
	return writeJSON(output, map[string]any{
		"total_chars":   total,
		"chinese_chars": chinese,
		"letters":       letters,
		"digits":        digits,
		"punctuation":   punctuation,
		"spaces":        spaces,
		"summary": "共 " + decimal(total) + " 个字符：汉字 " + decimal(chinese) +
			" 个、字母 " + decimal(letters) + " 个、数字 " + decimal(digits) +
			" 个、标点 " + decimal(punctuation) + " 个、空白 " + decimal(spaces) + " 个",
	})
}

type diffOperation struct {
	kind byte
	line string
}

func textDiff(input io.Reader, output io.Writer) error {
	var arguments struct {
		TextA string `json:"text_a"`
		TextB string `json:"text_b"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	operations, unchanged := lineDiff(splitLines(arguments.TextA), splitLines(arguments.TextB))
	added, removed := 0, 0
	for _, operation := range operations {
		switch operation.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	summary := "两版文本完全相同"
	if added != 0 || removed != 0 {
		summary = "相比旧版：新增 " + decimal(added) + " 行、删除 " +
			decimal(removed) + " 行、未改动 " + decimal(unchanged) + " 行"
	}
	return writeJSON(output, map[string]any{
		"added":     added,
		"removed":   removed,
		"unchanged": unchanged,
		"diff":      diffBody(operations, 1),
		"summary":   summary,
	})
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func lineDiff(before, after []string) ([]diffOperation, int) {
	lengths := make([][]int, len(before)+1)
	for index := range lengths {
		lengths[index] = make([]int, len(after)+1)
	}
	for left := len(before) - 1; left >= 0; left-- {
		for right := len(after) - 1; right >= 0; right-- {
			if before[left] == after[right] {
				lengths[left][right] = lengths[left+1][right+1] + 1
			} else {
				lengths[left][right] = max(lengths[left+1][right], lengths[left][right+1])
			}
		}
	}
	operations := make([]diffOperation, 0, len(before)+len(after))
	left, right, unchanged := 0, 0, 0
	for left < len(before) && right < len(after) {
		if before[left] == after[right] {
			operations = append(operations, diffOperation{' ', before[left]})
			left++
			right++
			unchanged++
		} else if lengths[left+1][right] >= lengths[left][right+1] {
			operations = append(operations, diffOperation{'-', before[left]})
			left++
		} else {
			operations = append(operations, diffOperation{'+', after[right]})
			right++
		}
	}
	for ; left < len(before); left++ {
		operations = append(operations, diffOperation{'-', before[left]})
	}
	for ; right < len(after); right++ {
		operations = append(operations, diffOperation{'+', after[right]})
	}
	return operations, unchanged
}

func diffBody(operations []diffOperation, contextLines int) []string {
	include := make([]bool, len(operations))
	for index, operation := range operations {
		if operation.kind == ' ' {
			continue
		}
		start := max(0, index-contextLines)
		end := min(len(operations), index+contextLines+1)
		for bodyIndex := start; bodyIndex < end; bodyIndex++ {
			include[bodyIndex] = true
		}
	}
	result := make([]string, 0, len(operations))
	for index, operation := range operations {
		if include[index] {
			result = append(result, string(operation.kind)+operation.line)
		}
	}
	return result
}

var extractPatterns = map[string]*regexp.Regexp{
	"email": regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	"phone": regexp.MustCompile(`(?:\+?[0-9]{1,3}[- ]?)?1[3-9][0-9]{9}`),
	"url":   regexp.MustCompile(`https?://[A-Za-z0-9._~:/?#@!$&'*+%=\-]+`),
	"date": regexp.MustCompile(
		`[0-9]{4}[-/年][0-9]{1,2}[-/月][0-9]{1,2}日?|[0-9]{1,2}[-/][0-9]{1,2}[-/][0-9]{4}`,
	),
}

func textExtract(input io.Reader, output io.Writer) error {
	var arguments struct {
		Text    string          `json:"text"`
		Pattern json.RawMessage `json:"pattern"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	patterns, err := decodePatterns(arguments.Pattern)
	if err != nil {
		return err
	}
	labels := map[string]string{
		"email": "邮箱", "phone": "手机号", "url": "链接", "date": "日期",
	}
	byPattern := make(map[string][]string, len(patterns))
	var matches []string
	summaries := make([]string, 0, len(patterns))
	for _, name := range patterns {
		pattern := extractPatterns[name]
		if pattern == nil {
			return errors.New("unknown pattern " + name)
		}
		found := pattern.FindAllString(arguments.Text, -1)
		if name == "phone" {
			found = phoneBoundaryMatches(arguments.Text, pattern)
		}
		if found == nil {
			found = []string{}
		}
		byPattern[name] = found
		matches = append(matches, found...)
		summaries = append(summaries, labels[name]+" "+decimal(len(found))+" 个")
	}
	if matches == nil {
		matches = []string{}
	}
	return writeJSON(output, map[string]any{
		"matches":    matches,
		"count":      len(matches),
		"by_pattern": byPattern,
		"summary":    strings.Join(summaries, "；"),
	})
}

func decodePatterns(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil, errors.New("pattern must not be empty")
		}
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil || len(multiple) == 0 {
		return nil, errors.New("pattern must be a non-empty string or list")
	}
	seen := make(map[string]bool, len(multiple))
	for _, name := range multiple {
		if name == "" || seen[name] {
			return nil, errors.New("pattern list must contain unique non-empty values")
		}
		seen[name] = true
	}
	return multiple, nil
}

func phoneBoundaryMatches(text string, pattern *regexp.Regexp) []string {
	indices := pattern.FindAllStringIndex(text, -1)
	result := make([]string, 0, len(indices))
	for _, location := range indices {
		if location[0] > 0 && isASCIIDigit(text[location[0]-1]) {
			continue
		}
		if location[1] < len(text) && isASCIIDigit(text[location[1]]) {
			continue
		}
		result = append(result, text[location[0]:location[1]])
	}
	return result
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
