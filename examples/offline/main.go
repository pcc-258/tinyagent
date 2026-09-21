// Command offline 用脚本化的假模型演示完整 agent 流程 ——
// 工具调用、事件流、Hook、审计 —— 全程不需要任何 API 凭据。
//
//	go run ./examples/offline
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"tinyagent"
)

// fakeModel 按脚本返回预设响应，让示例在没有外部 API 时也能跑通工具循环。
type fakeModel struct {
	turns []tinyagent.Response
	next  int
}

func (m *fakeModel) Generate(_ context.Context, _ tinyagent.Request) (tinyagent.Response, error) {
	if m.next >= len(m.turns) {
		return tinyagent.Response{
			Message: tinyagent.Message{Role: tinyagent.RoleAssistant, Content: "（脚本已结束）"},
		}, nil
	}
	resp := m.turns[m.next]
	m.next++
	return resp, nil
}

type esQueryArgs struct {
	Cluster string `json:"cluster" desc:"ES 集群名"`
	Index   string `json:"index" desc:"索引名"`
}

func main() {
	model := &fakeModel{turns: []tinyagent.Response{
		{Message: tinyagent.Message{
			Role: tinyagent.RoleAssistant,
			ToolCalls: []tinyagent.ToolCall{{
				ID:        "call-1",
				Name:      "query_es",
				Arguments: json.RawMessage(`{"cluster":"es-prod-3","index":"orders"}`),
			}},
		}},
		{Message: tinyagent.Message{
			Role:    tinyagent.RoleAssistant,
			Content: "es-prod-3 的 orders 索引共有 1204 个文档。",
		}},
	}}

	queryTool, err := tinyagent.NewFuncTool("query_es", "查询指定 ES 集群索引的文档数",
		func(_ context.Context, args esQueryArgs) (tinyagent.ToolResult, error) {
			return tinyagent.ToolResult{
				Content: fmt.Sprintf("cluster=%s index=%s docs=1204", args.Cluster, args.Index),
			}, nil
		})
	if err != nil {
		log.Fatal(err)
	}

	audit := tinyagent.NewMemoryAudit()
	var toolCalls int

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  model,
		System: "需要集群数据时调用 query_es 工具。",
		Tools:  []tinyagent.Tool{queryTool},
		Audit:  audit,
		Hooks: []tinyagent.Hook{tinyagent.HookFuncs{
			OnBeforeToolCall: func(_ context.Context, call *tinyagent.ToolCall) error {
				toolCalls++
				fmt.Printf("  [hook] 即将调用 %s\n", call.Name)
				return nil
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	fmt.Println("=== 事件流 ===")
	for ev, err := range ag.Run(ctx, "demo", "es-prod-3 的 orders 索引有多少文档？") {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case tinyagent.EventText:
			fmt.Printf("  [text] %s\n", ev.Text)
		case tinyagent.EventToolCall:
			fmt.Printf("  [tool_call] %s(%s)\n", ev.ToolCall.Name, ev.ToolCall.Arguments)
		case tinyagent.EventToolResult:
			fmt.Printf("  [tool_result] %s\n", ev.ToolResult.Content)
		case tinyagent.EventRunEnd:
			fmt.Println("  [run_end]")
		}
	}

	fmt.Printf("\nHook 观察到 %d 次工具调用\n", toolCalls)

	fmt.Println("\n=== 审计记录 ===")
	records, err := audit.Query(ctx, tinyagent.AuditQuery{SessionID: "demo"})
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range records {
		fmt.Printf("  %-14s %s\n", r.Kind, r.Component)
	}

	s, err := ag.Session(ctx, "demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n会话最终消息数：%d\n", s.Len())
}
