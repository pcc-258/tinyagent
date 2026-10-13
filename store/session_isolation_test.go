package store

import (
	"github.com/pcc-258/tinyagent/core"
	"testing"
)

func TestSessionOwnedDataDoesNotAlias(t *testing.T) {
	for _, mode := range []string{"append", "replace", "load"} {
		t.Run(mode, func(t *testing.T) {
			m := core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "original", Arguments: []byte(`{"x":1}`)}}, Meta: map[string]any{"key": "original"}}
			s := NewSession("s")
			switch mode {
			case "append":
				s.Append(m)
			case "replace":
				s.Replace([]core.Message{m})
			case "load":
				s.LoadFrom(SessionSnapshot{Messages: []core.Message{m}})
			}
			m.ToolCalls[0].ID = "input mutation"
			m.ToolCalls[0].Arguments[0] = 'X'
			m.Meta["key"] = "input mutation"
			snap := s.Snapshot()
			snap.Messages[0].ToolCalls[0].ID = "snapshot mutation"
			snap.Messages[0].ToolCalls[0].Arguments[0] = 'Y'
			snap.Messages[0].Meta["key"] = "snapshot mutation"
			got := s.Messages()[0]
			if got.ToolCalls[0].ID != "original" || string(got.ToolCalls[0].Arguments) != `{"x":1}` || got.Meta["key"] != "original" {
				t.Fatalf("aliased data=%+v", got)
			}
		})
	}
}
