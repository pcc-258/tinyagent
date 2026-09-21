// Command panicsafe 演示头号亮点：组件 panic 不会带崩主程序，且一定可见。
//
//	go run ./examples/panicsafe
package main

import (
	"context"
	"fmt"
	"log"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/tool"

	"github.com/pcc-258/tinyagent"
)

type emptyArgs struct{}

type boomModel struct{ next int }

func (m *boomModel) Generate(_ context.Context, _ model.Request) (model.Response, error) {
	m.next++
	if m.next == 1 {
		return model.Response{Message: core.Message{
			Role:      core.RoleAssistant,
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "explode", Arguments: []byte(`{}`)}},
		}}, nil
	}
	return model.Response{Message: core.Message{
		Role:    core.RoleAssistant,
		Content: "工具崩了，但运行还在继续。",
	}}, nil
}

func main() {
	boom, err := tool.NewFuncTool("explode", "总会 panic 的工具",
		func(_ context.Context, _ emptyArgs) (core.ToolResult, error) {
			panic("模拟工具内部崩溃")
		})
	if err != nil {
		log.Fatal(err)
	}

	ag, err := tinyagent.New(tinyagent.Config{
		Model: &boomModel{},
		Tools: []tool.Tool{boom},
	})
	if err != nil {
		log.Fatal(err)
	}

	for ev, err := range ag.Run(context.Background(), "demo", "调用 explode") {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case core.EventPanic:
			fmt.Printf("[panic 已捕获] 组件=%s 值=%v 堆栈=%d 字节\n",
				ev.Panic.Component, ev.Panic.Value, len(ev.Panic.Stack))
		case core.EventToolResult:
			fmt.Printf("[tool_result] 模型看到的软错误: %s\n", ev.ToolResult.Error)
		case core.EventText:
			fmt.Printf("[text] %s\n", ev.Text)
		}
	}
	fmt.Println("\n主程序仍然存活。")
}
