// Command tb2agent is a minimal Terminal-Bench 2 coding agent built on
// TinyAgent. It is a benchmark harness, not part of the TinyAgent library API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
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
	maxIterFlag := flag.Int("max-iterations", 32, "maximum agent loop iterations")
	timeoutSec := flag.Int("timeout-sec", 600, "total run timeout in seconds")
	maxTokens := flag.Int("max-tokens", 2048, "max output tokens per model call; 0 means no limit")
	traceModel := flag.Bool("trace-model", false, "log model responses for debugging")
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
		APIKey:    apiKey,
		BaseURL:   envOr("TINYAGENT_BASE_URL", "OPENAI_BASE_URL"),
		Model:     envOr("TINYAGENT_MODEL", "OPENAI_MODEL"),
		MaxTokens: *maxTokens,
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
		MaxIterations: *maxIterFlag,
		Timeout:       time.Duration(*timeoutSec) * time.Second,
		Hooks:         modelTraceHooks(*traceModel),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tb2agent: create agent:", err)
		os.Exit(1)
	}

	final, maxIterations, err := runAgent(context.Background(), ag, *instruction, func(ev core.Event) {
		switch ev.Type {
		case core.EventText:
			fmt.Print(ev.Text)
		case core.EventToolCall:
			fmt.Fprintf(os.Stderr, "[tool] %s %s\n", ev.ToolCall.Name, ev.ToolCall.Arguments)
		case core.EventToolResult:
			summary := ev.ToolResult.Content
			if ev.ToolResult.Error != "" {
				summary = "error: " + ev.ToolResult.Error
			}
			fmt.Fprintf(os.Stderr, "[tool-result] %s\n", truncateOutput(summary))
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tb2agent: run failed:", err)
		os.Exit(1)
	}
	if maxIterations {
		fmt.Fprintln(os.Stderr, "tb2agent: max iterations reached")
	}
	if strings.TrimSpace(final) == "" && !maxIterations {
		fmt.Fprintln(os.Stderr, "tb2agent: agent finished without a final answer")
		os.Exit(1)
	}
}

func modelTraceHooks(enabled bool) []hook.Hook {
	if !enabled {
		return nil
	}
	return []hook.Hook{hook.HookFuncs{
		OnAfterModelCall: func(_ context.Context, resp *model.Response) error {
			var names []string
			for _, tc := range resp.Message.ToolCalls {
				names = append(names, tc.Name)
			}
			tools := ""
			if len(names) > 0 {
				tools = strings.Join(names, ",")
			}
			fmt.Fprintf(os.Stderr, "[model] content=%q tools=%s finish=%s\n",
				truncateLine(resp.Message.Content), tools, resp.FinishReason)
			return nil
		},
	}}
}

func runAgent(
	ctx context.Context,
	ag *tinyagent.Agent,
	instruction string,
	onEvent func(core.Event),
) (string, bool, error) {
	var final strings.Builder
	maxIterations := false
	for ev, runErr := range ag.Run(ctx, "tb2", instruction) {
		if runErr != nil {
			if errors.Is(runErr, core.ErrMaxIterations) {
				maxIterations = true
				continue
			}
			return final.String(), false, runErr
		}
		if onEvent != nil {
			onEvent(ev)
		}
		if ev.Type == core.EventText {
			final.WriteString(ev.Text)
		}
	}
	return final.String(), maxIterations, nil
}

func envOr(primary, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(fallback)
}
