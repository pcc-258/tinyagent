// Command tb2agent is a minimal Terminal-Bench 2 coding agent built on
// TinyAgent. It is a benchmark harness, not part of the TinyAgent library API.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model/openai"
)

const version = "0.1.0"

const defaultSystemPrompt = `You are a coding agent running inside a disposable Linux container.
Your job is to complete the task by inspecting and modifying files and running commands.
Use the provided tools instead of assuming anything about the repository.
Work inside the current working directory, which is usually /app.
Do not ask the user questions and do not stop early: run tests or the verifier when available,
fix failures, and only report a concise final summary when the task is complete.`

func main() {
	instruction := flag.String("instruction", "", "task instruction for the agent")
	system := flag.String("system", defaultSystemPrompt, "system prompt")
	workdir := flag.String("workdir", ".", "working directory for file and shell tools")
	maxIterations := flag.Int("max-iterations", 32, "maximum agent loop iterations")
	timeoutSec := flag.Int("timeout-sec", 600, "total run timeout in seconds")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *instruction == "" {
		fmt.Fprintln(os.Stderr, "tb2agent: -instruction is required")
		flag.Usage()
		os.Exit(2)
	}

	apiKey := envOr("TINYAGENT_API_KEY", "OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "tb2agent: set TINYAGENT_API_KEY or OPENAI_API_KEY")
		os.Exit(2)
	}
	mdl := openai.New(openai.Config{
		APIKey:  apiKey,
		BaseURL: envOr("TINYAGENT_BASE_URL", "OPENAI_BASE_URL"),
		Model:   envOr("TINYAGENT_MODEL", "OPENAI_MODEL"),
	})

	tools, err := newCodingTools(*workdir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tb2agent: build tools:", err)
		os.Exit(1)
	}

	ag, err := tinyagent.New(tinyagent.Config{
		Model:         mdl,
		System:        *system,
		Tools:         tools,
		MaxIterations: *maxIterations,
		Timeout:       time.Duration(*timeoutSec) * time.Second,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tb2agent: create agent:", err)
		os.Exit(1)
	}

	var final strings.Builder
	for ev, runErr := range ag.Run(context.Background(), "tb2", *instruction) {
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "tb2agent: run failed:", runErr)
			os.Exit(1)
		}
		switch ev.Type {
		case core.EventText:
			fmt.Print(ev.Text)
			final.WriteString(ev.Text)
		case core.EventToolCall:
			fmt.Fprintf(os.Stderr, "[tool] %s %s\n", ev.ToolCall.Name, ev.ToolCall.Arguments)
		case core.EventToolResult:
			summary := ev.ToolResult.Content
			if ev.ToolResult.Error != "" {
				summary = "error: " + ev.ToolResult.Error
			}
			fmt.Fprintf(os.Stderr, "[tool-result] %s\n", truncateOutput(summary))
		}
	}
	if strings.TrimSpace(final.String()) == "" {
		fmt.Fprintln(os.Stderr, "tb2agent: agent finished without a final answer")
		os.Exit(1)
	}
}

func envOr(primary, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(fallback)
}
