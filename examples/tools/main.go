// Command tools 演示注册工具、让 agent 调用并消费事件流。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"tinyagent"
	"tinyagent/model/openai"
)

type weatherArgs struct {
	City string `json:"city" desc:"城市名"`
}

func main() {
	model := openai.New(openai.Config{
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		Model:   os.Getenv("OPENAI_MODEL"),
	})

	weather, err := tinyagent.NewFuncTool("get_weather", "查询指定城市的当前天气",
		func(ctx context.Context, args weatherArgs) (tinyagent.ToolResult, error) {
			return tinyagent.ToolResult{
				Content: fmt.Sprintf("%s 今天晴，22°C", args.City),
			}, nil
		})
	if err != nil {
		log.Fatal(err)
	}

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  model,
		System: "需要天气信息时调用 get_weather 工具。",
		Tools:  []tinyagent.Tool{weather},
	})
	if err != nil {
		log.Fatal(err)
	}

	for ev, err := range ag.Run(context.Background(), "demo", "北京今天天气怎么样？") {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case tinyagent.EventText:
			fmt.Print(ev.Text)
		case tinyagent.EventToolCall:
			fmt.Printf("\n[调用工具 %s]\n", ev.ToolCall.Name)
		case tinyagent.EventToolResult:
			fmt.Printf("[工具结果] %s\n", ev.ToolResult.Content)
		}
	}
	fmt.Println()
}
