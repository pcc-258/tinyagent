// Command tools 演示注册工具、让 agent 调用并消费事件流。
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/examples/internal/config"
	"github.com/pcc-258/tinyagent/model/openai"
	"github.com/pcc-258/tinyagent/tool"
)

type weatherArgs struct {
	City string `json:"city" desc:"城市名"`
}

func main() {
	cfg, err := config.Load(config.DefaultPath)
	if err != nil {
		log.Fatal(err)
	}

	mdl := openai.New(openai.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	})

	weather, err := tool.NewFuncTool("get_weather", "查询指定城市的当前天气",
		func(ctx context.Context, args weatherArgs) (core.ToolResult, error) {
			return core.ToolResult{
				Content: fmt.Sprintf("%s 今天晴，22°C", args.City),
			}, nil
		})
	if err != nil {
		log.Fatal(err)
	}

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  mdl,
		System: "需要天气信息时调用 get_weather 工具。",
		Tools:  []tool.Tool{weather},
	})
	if err != nil {
		log.Fatal(err)
	}

	for ev, err := range ag.Run(context.Background(), "demo", "北京今天天气怎么样？") {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case core.EventText:
			fmt.Print(ev.Text)
		case core.EventToolCall:
			fmt.Printf("\n[调用工具 %s]\n", ev.ToolCall.Name)
		case core.EventToolResult:
			fmt.Printf("[工具结果] %s\n", ev.ToolResult.Content)
		}
	}
	fmt.Println()
}
