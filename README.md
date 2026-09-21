# TinyAgent

[![CI](https://github.com/pcc-258/tinyagent/actions/workflows/ci.yml/badge.svg)](https://github.com/pcc-258/tinyagent/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/pcc-258/tinyagent.svg)](https://pkg.go.dev/github.com/pcc-258/tinyagent)
[![Go Report Card](https://goreportcard.com/badge/github.com/pcc-258/tinyagent)](https://goreportcard.com/report/github.com/pcc-258/tinyagent)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

An embeddable AI agent kernel for Go. Import it, and a few lines of code give your
program a complete agent loop — tool calling, context management, hooks, and audit —
with no CLI, no sidecar, and no IPC.

## Features

- **In-process** — a plain Go library. No CLI, no daemon, no external agent process.
- **Panic-safe** — a panic raised inside a tool, hook, or model adapter is recovered,
  converted into an error, and reported. It never takes down your program.
- **Zero-dependency core** — the core packages use the standard library only.
- **Fully replaceable** — every module is an interface. Swap the store, the context
  strategy, the hooks, or the entire agent loop.
- **Streaming-first** — token-by-token events, with tool-call assembly handled for you.

## Install

```bash
go get github.com/pcc-258/tinyagent
```

Requires Go 1.26+.

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/model/openai"
)

func main() {
	mdl := openai.New(openai.Config{
		APIKey:  "sk-...",                      // or set OPENAI_API_KEY
		BaseURL: "https://api.deepseek.com/v1", // any OpenAI-compatible endpoint
		Model:   "deepseek-v4-flash",
	})

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  mdl,
		System: "You are a concise assistant.",
	})
	if err != nil {
		log.Fatal(err)
	}

	reply, err := ag.Chat(context.Background(), "session-1", "Explain Go in one sentence.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(reply)
}
```

## Tools

Register a tool from any typed Go function. Its JSON schema is derived from the
parameter struct.

```go
type weatherArgs struct {
	City string `json:"city" desc:"City name"`
}

weather, err := tool.NewFuncTool("get_weather", "Get the current weather for a city",
	func(ctx context.Context, args weatherArgs) (core.ToolResult, error) {
		return core.ToolResult{Content: args.City + ": sunny, 22°C"}, nil
	})

ag, _ := tinyagent.New(tinyagent.Config{
	Model: mdl,
	Tools: []tool.Tool{weather},
})
```

## Streaming Events

```go
for ev, err := range ag.Run(ctx, "session-1", "What's the weather in Beijing?") {
	if err != nil {
		log.Fatal(err)
	}
	switch ev.Type {
	case core.EventText:
		fmt.Print(ev.Text)
	case core.EventToolCall:
		fmt.Printf("\n[calling %s]\n", ev.ToolCall.Name)
	case core.EventToolResult:
		fmt.Printf("[result] %s\n", ev.ToolResult.Content)
	}
}
```

## Examples

| Example | Description | API key |
|---|---|---|
| [`offline`](examples/offline) | Full flow: tools, hooks, event stream, audit | No |
| [`panicsafe`](examples/panicsafe) | A panicking tool does not crash the host | No |
| [`customrunner`](examples/customrunner) | Replace the agent loop entirely | No |
| [`quickstart`](examples/quickstart) | Minimal integration | Yes |
| [`tools`](examples/tools) | Tool registration and event consumption | Yes |

Run the offline examples with no credentials:

```bash
go run ./examples/offline
go run ./examples/panicsafe
go run ./examples/customrunner
```

The two API-backed examples read credentials from `examples/config.yaml`:

```bash
cp examples/config.yaml.example examples/config.yaml
# then edit api_key / base_url / model
```

`examples/config.yaml` is gitignored.

## Packages

| Package | Role |
|---|---|
| `tinyagent` | Facade: `Agent` + `Config`, assembles the modules |
| `tinyagent/core` | Data model: `Message`, `Event`, `Usage`, `Error` |
| `tinyagent/model` | `Model` / `StreamingModel` interfaces |
| `tinyagent/model/openai` | OpenAI-compatible adapter |
| `tinyagent/tool` | `Tool` interface, generic registration, schema |
| `tinyagent/store` | `Store` interface, in-memory implementation, `Session` |
| `tinyagent/ctxmgr` | Context management strategies |
| `tinyagent/hook` | Synchronous interception points |
| `tinyagent/audit` | Immutable record of agent behavior |
| `tinyagent/runner` | Agent loop |

Most users only need `github.com/pcc-258/tinyagent`; import subpackages when
implementing a custom module.

## Panic Safety

TinyAgent recovers panics raised inside component callbacks — tools, hooks, and model
adapters, including the built-in ones — converts them into errors, and reports them
through three channels: the event stream, the audit log, and an optional callback.
Nothing is swallowed silently.

Process-level failures (`os.Exit`, `runtime.Goexit`, stack overflow, OOM, cgo
segfaults) cannot be recovered by any in-process library and are out of scope.

## License

[MIT](LICENSE) © 2026 pcc-258
