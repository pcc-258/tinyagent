// Command customrunner 演示第 5 条主张：整个 agent loop 都可以被替换。
//
//	go run ./examples/customrunner
package main

import (
	"context"
	"fmt"
	"iter"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"

	"github.com/pcc-258/tinyagent"
)

type fixedModel struct{}

func (fixedModel) Generate(_ context.Context, _ model.Request) (model.Response, error) {
	return model.Response{Message: core.Message{Role: core.RoleAssistant, Content: "来自模型"}}, nil
}

// echoRunner 是一个极简的自定义 Runner：不调用模型，直接回显输入。
// 它满足 Runner 接口，因此可以整体替换内置的 ReActRunner。
type echoRunner struct{}

func (echoRunner) Run(_ context.Context, _ *store.Session, input string) iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		yield(core.Event{Type: core.EventRunStart}, nil)
		yield(core.Event{Type: core.EventText, Text: "echo: " + input}, nil)
		yield(core.Event{Type: core.EventRunEnd}, nil)
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
