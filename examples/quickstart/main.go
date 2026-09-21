// Command quickstart 演示 TinyAgent 的最小接入方式。
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/examples/internal/config"
	"github.com/pcc-258/tinyagent/model/openai"
)

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

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  mdl,
		System: cfg.System,
	})
	if err != nil {
		log.Fatal(err)
	}

	reply, err := ag.Chat(context.Background(), "demo", "用一句话介绍 Go 语言。")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(reply)
}
