package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	contractStart = "<!-- ecs-skill-contract:start -->"
	contractEnd   = "<!-- ecs-skill-contract:end -->"
)

type manifest struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Dependencies struct {
		Tools []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"tools"`
		Capabilities []string `json:"capabilities"`
	} `json:"dependencies"`
	Failure struct {
		MaxAttempts int    `json:"max_attempts"`
		Timeout     string `json:"timeout"`
		Fallback    string `json:"fallback"`
	} `json:"failure"`
	Risk struct {
		Level      string   `json:"level"`
		Approval   string   `json:"approval"`
		Boundaries []string `json:"boundaries"`
	} `json:"risk"`
	Compatibility struct {
		ECS          string `json:"ecs"`
		ToolProtocol string `json:"tool_protocol"`
		MinModel     string `json:"min_model"`
	} `json:"compatibility"`
	Reuse struct {
		Value string   `json:"value"`
		Tags  []string `json:"tags"`
	} `json:"reuse"`
	Compensation struct {
		Required bool     `json:"required"`
		Steps    []string `json:"steps"`
	} `json:"compensation"`
}

func main() {
	check := flag.Bool("check", false, "verify without changing files")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	paths, err := filepath.Glob(filepath.Join(*root, "skills.d", "*", "skill.json"))
	if err != nil {
		fatal(err)
	}
	sort.Strings(paths)
	var stale []string
	for _, manifestPath := range paths {
		updated, docPath, err := syncOne(manifestPath, *check)
		if err != nil {
			fatal(err)
		}
		if updated {
			stale = append(stale, docPath)
		}
	}
	if *check && len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "Skill contract documentation is stale:")
		for _, path := range stale {
			fmt.Fprintln(os.Stderr, " -", path)
		}
		fmt.Fprintln(os.Stderr, "run: make sync-skill-docs")
		os.Exit(1)
	}
	if *check {
		fmt.Printf("Verified %d Skill document contracts.\n", len(paths))
	} else {
		fmt.Printf("Synchronized %d Skill document contracts.\n", len(paths))
	}
}

func syncOne(manifestPath string, check bool) (bool, string, error) {
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return false, "", err
	}
	var item manifest
	if err := json.Unmarshal(rawManifest, &item); err != nil {
		return false, "", fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	docPath := filepath.Join(filepath.Dir(manifestPath), "SKILL.md")
	rawDoc, err := os.ReadFile(docPath)
	if err != nil {
		return false, docPath, err
	}
	updated, err := replaceContract(rawDoc, renderContract(item))
	if err != nil {
		return false, docPath, fmt.Errorf("update %s: %w", docPath, err)
	}
	if bytes.Equal(rawDoc, updated) {
		return false, docPath, nil
	}
	if check {
		return true, docPath, nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(docPath), ".SKILL.md-*")
	if err != nil {
		return false, docPath, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(updated); err != nil {
		temporary.Close()
		return false, docPath, err
	}
	if err := temporary.Close(); err != nil {
		return false, docPath, err
	}
	if err := os.Rename(temporaryPath, docPath); err != nil {
		return false, docPath, err
	}
	return true, docPath, nil
}

func replaceContract(document, contract []byte) ([]byte, error) {
	text := strings.TrimRight(string(document), "\n")
	start := strings.Index(text, contractStart)
	end := strings.Index(text, contractEnd)
	if (start >= 0) != (end >= 0) {
		return nil, fmt.Errorf("contract markers are incomplete")
	}
	if start >= 0 {
		if end < start {
			return nil, fmt.Errorf("contract markers are reversed")
		}
		end += len(contractEnd)
		text = strings.TrimRight(text[:start], "\n") + "\n\n" + string(contract) + strings.TrimLeft(text[end:], "\n")
	} else {
		text += "\n\n## 工程契约\n\n" + string(contract)
	}
	return []byte(strings.TrimRight(text, "\n") + "\n"), nil
}

func renderContract(item manifest) []byte {
	var output strings.Builder
	fmt.Fprintln(&output, contractStart)
	fmt.Fprintln(&output, "### 依赖工具")
	if len(item.Dependencies.Tools) == 0 {
		fmt.Fprintln(&output, "- 不依赖额外 Tool。")
	} else {
		for _, tool := range item.Dependencies.Tools {
			fmt.Fprintf(&output, "- `%s %s`\n", tool.Name, tool.Version)
		}
	}
	if len(item.Dependencies.Capabilities) == 0 {
		fmt.Fprintln(&output, "- 节点能力：无额外要求。")
	} else {
		fmt.Fprintf(&output, "- 节点能力：%s。\n", codeList(item.Dependencies.Capabilities))
	}

	fmt.Fprintln(&output, "\n### 失败处理")
	fmt.Fprintf(&output, "- 最多尝试 %d 次，单次超时 `%s`；失败后%s。\n", item.Failure.MaxAttempts, item.Failure.Timeout, sentence(item.Failure.Fallback))
	fmt.Fprintf(&output, "- 风险等级 `%s`，审批策略 `%s`", item.Risk.Level, item.Risk.Approval)
	if len(item.Risk.Boundaries) > 0 {
		fmt.Fprintf(&output, "；边界：%s", strings.Join(item.Risk.Boundaries, "；"))
	}
	fmt.Fprintln(&output, "。")
	if item.Compensation.Required {
		fmt.Fprintf(&output, "- 需要补偿：%s。\n", strings.Join(item.Compensation.Steps, "；"))
	} else {
		fmt.Fprintln(&output, "- 不需要补偿；失败不会留下需要回滚的外部副作用。")
	}

	fmt.Fprintln(&output, "\n### 复用价值")
	fmt.Fprintf(&output, "- %s。\n", sentence(item.Reuse.Value))
	if len(item.Reuse.Tags) > 0 {
		fmt.Fprintf(&output, "- 标签：%s。\n", codeList(item.Reuse.Tags))
	}

	fmt.Fprintln(&output, "\n### 版本演进")
	fmt.Fprintf(&output, "- Skill `%s v%s`；要求 ECS `%s`、Tool Protocol `%s`、最低模型 `%s`。\n", item.Name, item.Version, item.Compatibility.ECS, item.Compatibility.ToolProtocol, item.Compatibility.MinModel)
	fmt.Fprintln(&output, "- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。")
	fmt.Fprintln(&output, contractEnd)
	return []byte(output.String())
}

func codeList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "`" + value + "`"
	}
	return strings.Join(quoted, "、")
}

func sentence(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "。；;.!！")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
