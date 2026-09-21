// Command customrunner 演示第 5 条主张：整个 agent loop 都可以被替换。
//
//	go run ./examples/customrunner
package main

import (
	"context"
	"fmt"
	"iter"

	"tinyagent"
)

type fixedModel struct{}

func (fixedModel) Generate(_ context.Context, _ tinyagent.Request) (tinyagent.Response, error) {
	return tinyagent.Response{Message: tinyagent.Message{Role: tinyagent.RoleAssistant, Content: "来自模型"}}, nil
}

// echoRunner 是一个极简的自定义 Runner：不调用模型，直接回显输入。
// 它满足 Runner 接口，因此可以整体替换内置的 ReActRunner。
type echoRunner struct{}

func (echoRunner) Run(_ context.Context, _ *tinyagent.Session, input string) iter.Seq2[tinyagent.Event, error] {
	return func(yield func(tinyagent.Event, error) bool) {
		yield(tinyagent.Event{Type: tinyagent.EventRunStart}, nil)
		yield(tinyagent.Event{Type: tinyagent.EventText, Text: "echo: " + input}, nil)
		yield(tinyagent.Event{Type: tinyagent.EventRunEnd}, nil)
	}
}

func main() {
	ag, err := tinyagent.New(tinyagent.Config{
		Model:  fixedModel{},
		Runner: echoRunner{},
	})
	if err != nil {
		panic(err)
	}

	reply, err := ag.Chat(context.Background(), "demo", "你好")
	if err != nil {
		panic(err)
	}
	fmt.Printf("自定义 Runner 输出: %s\n", reply)
}
