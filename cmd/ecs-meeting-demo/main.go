package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ecs/internal/meetingdemo"
)

func main() {
	root := flag.String("root", ".", "ECS repository root")
	output := flag.String(
		"out",
		"data/demo/meeting-followup",
		"directory for downloadable evidence JSON",
	)
	repeat := flag.Int("repeat", 5, "number of consecutive runs per scenario")
	sourceRevision := flag.String(
		"source-revision",
		os.Getenv("ECS_SOURCE_REVISION"),
		"source revision recorded in summary.json",
	)
	flag.Parse()
	if *repeat < 1 {
		fmt.Fprintln(os.Stderr, "repeat must be positive")
		os.Exit(2)
	}
	orderDirectory := filepath.Join(*output, "orders")
	runner, err := meetingdemo.New(*root, orderDirectory)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*output, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type summary struct {
		Scenario        string `json:"scenario"`
		Iteration       int    `json:"iteration"`
		Status          string `json:"status"`
		Evidence        string `json:"evidence"`
		StableSignature string `json:"stable_signature"`
	}
	type summaryDocument struct {
		SchemaVersion  string    `json:"schema_version"`
		SourceRevision string    `json:"source_revision,omitempty"`
		GeneratedAt    string    `json:"generated_at"`
		Runs           []summary `json:"runs"`
		Result         string    `json:"result"`
	}
	summaries := make([]summary, 0, *repeat*2)
	for iteration := 1; iteration <= *repeat; iteration++ {
		for _, scenario := range []string{"success", "failure"} {
			evidence, err := runner.Run(context.Background(), scenario, iteration)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			data, err := json.MarshalIndent(evidence, "", "  ")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			path := filepath.Join(*output, fmt.Sprintf("%s-%d.json", scenario, iteration))
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			summaries = append(summaries, summary{
				Scenario: scenario, Iteration: iteration, Status: evidence.Status,
				Evidence: filepath.Base(path), StableSignature: evidence.StableSignature,
			})
		}
	}
	document := summaryDocument{
		SchemaVersion:  "ecs.meeting-demo-summary/v1",
		SourceRevision: strings.TrimSpace(*sourceRevision),
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Runs:           summaries,
		Result:         "success and failure scenarios passed " + fmt.Sprint(*repeat) + " times each",
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(*output, "summary.json"), append(data, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := writeChecksums(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func writeChecksums(output string) error {
	files := make([]string, 0)
	err := filepath.Walk(output, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || info.Name() == "SHA256SUMS" {
			return nil
		}
		relative, err := filepath.Rel(output, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	var checksums strings.Builder
	for _, relative := range files {
		data, err := os.ReadFile(filepath.Join(output, relative))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&checksums, "%x  %s\n", digest, filepath.ToSlash(relative))
	}
	return os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(checksums.String()), 0o600)
}
