package ctxmgr

import (
	"context"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/tool"
	"unicode"
)

// SimpleCounter 用字符数启发式估算 token 数量。
//
// 这是零依赖的粗略估算：英文约 4 字符 / token，CJK 约 1.5 字符 / token。
// 需要精确计数时，接入方应实现 Counter 接口接入对应 tokenizer。
type SimpleCounter struct {
	// CharsPerToken 是英文的字符/token 比，零值时取 4。
	CharsPerToken float64
	// CJKCharsPerToken 是 CJK 字符的字符/token 比，零值时取 1.5。
	CJKCharsPerToken float64
}

// NewSimpleCounter 返回使用默认比值的计数器。
func NewSimpleCounter() *SimpleCounter { return &SimpleCounter{} }

// Count 实现 Counter。
//
// 同时覆盖消息、工具 schema 与系统提示 —— 只算消息会严重低估，
// 工具定义本身往往很耗 token。
func (c *SimpleCounter) Count(ctx context.Context, req CountRequest) (CountResult, error) {
	if err := ctx.Err(); err != nil {
		return CountResult{}, err
	}
	byPart := map[string]int{
		"messages": c.estimateMessages(req.Messages),
		"tools":    c.estimateTools(req.Tools),
		"system":   c.estimate(req.System),
	}
	total := 0
	for _, v := range byPart {
		total += v
	}
	return CountResult{Total: total, ByPart: byPart}, nil
}

func (c *SimpleCounter) estimateMessages(msgs []core.Message) int {
	n := 0
	for _, m := range msgs {
		n += 4 // 每条消息的固定开销
		n += c.estimate(string(m.Role))
		n += c.estimate(m.Content)
		n += c.estimate(m.Name)
		for _, tc := range m.ToolCalls {
			n += c.estimate(tc.Name)
			n += c.estimate(string(tc.Arguments))
		}
	}
	return n
}

func (c *SimpleCounter) estimateTools(tools []tool.ToolInfo) int {
	n := 0
	for _, t := range tools {
		n += c.estimate(t.Name)
		n += c.estimate(t.Description)
		n += c.estimate(string(t.Parameters))
	}
	return n
}

func (c *SimpleCounter) estimate(s string) int {
	if s == "" {
		return 0
	}
	perToken := c.CharsPerToken
	if perToken <= 0 {
		perToken = 4
	}
	cjkPerToken := c.CJKCharsPerToken
	if cjkPerToken <= 0 {
		cjkPerToken = 1.5
	}

	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return int(float64(cjk)/cjkPerToken+float64(other)/perToken) + 1
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
