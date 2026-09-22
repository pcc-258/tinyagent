package main

import (
	"context"
	"testing"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/tool"
)

type loopingModel struct{}

func (loopingModel) Generate(_ context.Context, _ model.Request) (model.Response, error) {
	return model.Response{
		Message: core.Message{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{
				ID:        "c1",
				Name:      "noop",
				Arguments: []byte(`{}`),
			}},
		},
	}, nil
}

// TestRunAgent_MaxIterationsIsNotFatal verifies the benchmark CLI does not
// convert a reached iteration budget into a process failure; Harbor should
// still run the verifier and judge the resulting filesystem state.
func TestRunAgent_MaxIterationsIsNotFatal(t *testing.T) {
	type emptyArgs struct{}
	noop, err := tool.NewFuncTool("noop", "no-op", func(_ context.Context, _ emptyArgs) (core.ToolResult, error) {
		return core.ToolResult{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := tinyagent.New(tinyagent.Config{
		Model:         loopingModel{},
		Tools:         []tool.Tool{noop},
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	final, maxIterations, err := runAgent(context.Background(), ag, "go", nil)
	if err != nil {
		t.Fatalf("max iterations must not be fatal: %v", err)
	}
	if !maxIterations {
		t.Error("expected maxIterations=true")
	}
	if final != "" {
		t.Errorf("final = %q, want empty", final)
	}
}
