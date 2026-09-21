package tinyagent

import (
	"context"
	"iter"
)

// Request 是一次模型调用请求。
type Request struct {
	// Messages 是本次要发送的消息序列。
	Messages []Message
	// Tools 是可用工具的定义。
	Tools []ToolInfo
	// Model 是模型名；为空时由适配器使用默认值。
	Model string
	// Temperature 是采样温度；nil 表示用适配器默认值。
	Temperature *float64
	// MaxTokens 是输出上限；nil 表示用适配器默认值。
	MaxTokens *int
	// Meta 是适配器自定义参数。
	Meta map[string]any
}

// Response 是一次模型调用响应。
type Response struct {
	// Message 是模型的回复，可能带有 ToolCalls。
	Message Message
	// Usage 是本次调用的 token 用量。
	Usage Usage
	// FinishReason 是结束原因，如 "stop" / "tool_calls" / "length"。
	FinishReason string
}

// ToolCallDelta 是流式响应中工具调用的增量片段。
//
// 流式工具调用的 arguments 是分片到达的，调用方需要按 Index 聚合拼接。
type ToolCallDelta struct {
	// Index 标识这是本次响应中的第几个工具调用。
	Index int
	// ID 通常只在首片出现。
	ID string
	// Name 通常只在首片出现。
	Name string
	// ArgumentsDelta 是参数片段，需要按 Index 累加拼接。
	ArgumentsDelta string
}

// Chunk 是流式响应的一块。
type Chunk struct {
	// TextDelta 是文本增量。
	TextDelta string
	// ToolCall 是工具调用增量。
	ToolCall *ToolCallDelta
	// Usage 通常在最后一块出现。
	Usage *Usage
	// FinishReason 在最后一块出现。
	FinishReason string
}

// Model 是与 LLM 交互的边界。
//
// 只需要实现非流式生成。若同时实现 StreamingModel，
// 运行循环会通过类型断言探测并优先使用流式。
type Model interface {
	Generate(ctx context.Context, req Request) (Response, error)
}

// StreamingModel 是可选的流式能力。
type StreamingModel interface {
	Model
	Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error]
}
