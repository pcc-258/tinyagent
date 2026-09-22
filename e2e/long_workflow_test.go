package e2e_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

type capturingModel struct {
	responses []model.Response
	requests  []model.Request
}

func (m *capturingModel) Generate(_ context.Context, req model.Request) (model.Response, error) {
	m.requests = append(m.requests, req)
	if len(m.responses) == 0 {
		return model.Response{Message: core.Message{Role: core.RoleAssistant, Content: "fallback"}}, nil
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

// TestAgentEndToEndLongContextBudget verifies the public Agent facade trims a
// long session before the model sees it and emits context actions.
func TestAgentEndToEndLongContextBudget(t *testing.T) {
	mdl := &capturingModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, Content: "ok"}},
	}}
	st := store.NewMemoryStore()
	counter := ctxmgr.NewSimpleCounter()
	ag, err := tinyagent.New(tinyagent.Config{
		Model:         mdl,
		Store:         st,
		Counter:       counter,
		Context:       ctxmgr.NewDefaultContextManager(counter),
		ContextBudget: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	s, err := ag.Session(ctx, "long")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 200)})
	}

	events, err := collectRun(ag.Run(ctx, "long", "finish"))
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(events, core.EventContextAction) == 0 {
		t.Fatal("expected at least one context action under a tight budget")
	}
	if len(mdl.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(mdl.requests))
	}
	if got := len(mdl.requests[0].Messages); got >= 41 {
		t.Errorf("model saw %d messages, want fewer after context trimming", got)
	}
}

// TestAgentEndToEndLongToolChain verifies the facade handles a long sequence of
// tool calls without losing order or hitting premature termination.
func TestAgentEndToEndLongToolChain(t *testing.T) {
	const nToolCalls = 15
	responses := make([]model.Response, 0, nToolCalls+1)
	for i := 0; i < nToolCalls; i++ {
		responses = append(responses, model.Response{
			Message: core.Message{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{{
					ID:        fmt.Sprintf("c%d", i),
					Name:      "noop",
					Arguments: []byte(`{}`),
				}},
			},
		})
	}
	responses = append(responses, model.Response{
		Message: core.Message{Role: core.RoleAssistant, Content: "done"},
	})

	type emptyArgs struct{}
	noop, err := tool.NewFuncTool("noop", "no-op", func(_ context.Context, _ emptyArgs) (core.ToolResult, error) {
		return core.ToolResult{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := tinyagent.New(tinyagent.Config{
		Model:         &scriptedModel{responses: responses},
		Tools:         []tool.Tool{noop},
		MaxIterations: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	var reply strings.Builder
	events, err := collectRun(ag.Run(context.Background(), "long-task", "go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == core.EventText {
			reply.WriteString(ev.Text)
		}
	}
	if countEventType(events, core.EventToolCall) != nToolCalls {
		t.Errorf("tool calls = %d, want %d", countEventType(events, core.EventToolCall), nToolCalls)
	}
	if countEventType(events, core.EventToolResult) != nToolCalls {
		t.Errorf("tool results = %d, want %d", countEventType(events, core.EventToolResult), nToolCalls)
	}
	if reply.String() != "done" {
		t.Errorf("reply = %q, want done", reply.String())
	}
}
