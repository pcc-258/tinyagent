package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model/openai"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

// Exercise real HTTP/SSE, context offload, the automatically wired retrieval
// tool, streaming usage and committed history through the public facade.
func TestHTTPStreamOffloadReadbackAndUsage(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages      []core.Message `json:"messages"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if !req.StreamOptions.IncludeUsage {
			t.Error("stream did not request usage")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch turn {
		case 0:
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"large\",\"function\":{\"name\":\"large_result\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		case 1:
			var ref string
			for _, m := range req.Messages {
				if m.Role == core.RoleTool && strings.HasPrefix(m.Content, "[offloaded:") {
					ref = strings.Split(strings.TrimPrefix(m.Content, "[offloaded:"), "]")[0]
				}
			}
			if ref == "" {
				t.Error("model did not receive offload reference")
				return
			}
			args, _ := json.Marshal(map[string]any{"ref": ref, "limit": 128})
			encodedArgs, _ := json.Marshal(string(args))
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read\",\"function\":{\"name\":\"read_offloaded\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", encodedArgs)
		case 2:
			last := req.Messages[len(req.Messages)-1]
			if last.Role != core.RoleTool || !strings.Contains(last.Content, "next_offset=128") || !strings.Contains(last.Content, "actual data") {
				t.Errorf("readback failed: %+v", last)
			}
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		default:
			t.Error("unexpected extra model turn")
		}
		turn++
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	type empty struct{}
	large, err := tool.NewFuncTool("large_result", "Return a large result", func(context.Context, empty) (core.ToolResult, error) {
		return core.ToolResult{Content: strings.Repeat("actual data ", 4000)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryStore()
	ag, err := tinyagent.New(tinyagent.Config{Model: openai.New(openai.Config{BaseURL: srv.URL}), Tools: []tool.Tool{large}, Store: st, ContextBudget: 1500, OutputReserve: 100})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	var text strings.Builder
	for ev, err := range ag.Run(context.Background(), "roundtrip", "go") {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == core.EventText {
			text.WriteString(ev.Text)
		}
		if ev.Type == core.EventRunEnd {
			total = ev.Usage.TotalTokens
			snap, err := st.Load(context.Background(), "roundtrip")
			if err != nil || len(snap.Messages) != 6 {
				t.Fatalf("history not committed: %+v %v", snap, err)
			}
		}
	}
	if total != 36 || text.String() != "done" || turn != 3 {
		t.Fatalf("total=%d text=%q turns=%d", total, text.String(), turn)
	}
}
