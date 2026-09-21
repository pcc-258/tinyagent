// Command quickstart 演示 TinyAgent 的最小接入方式。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"tinyagent"
	"tinyagent/model/openai"
)

func main() {
	model := openai.New(openai.Config{
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		Model:   os.Getenv("OPENAI_MODEL"),
	})

	ag, err := tinyagent.New(tinyagent.Config{
		Model:  model,
		System: "你是一个简洁的助手。",
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
