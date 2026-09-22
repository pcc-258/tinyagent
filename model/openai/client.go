package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/tool"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	defaultTimeout = 60 * time.Second
)

// Config 配置一个 OpenAI 兼容客户端。
type Config struct {
	// APIKey 为空时读取环境变量 OPENAI_API_KEY。
	APIKey string
	// BaseURL 为空时使用 https://api.openai.com/v1。
	// 指向 DeepSeek / Ollama / vLLM 等兼容端点即可复用本客户端。
	BaseURL string
	// Model 是默认模型名，可被 Request.Model 覆盖。
	Model string
	// MaxTokens 是每次模型调用的默认输出 token 上限；<=0 表示不限制。
	MaxTokens int
	// HTTPClient 为空时使用带超时的默认客户端。
	HTTPClient *http.Client
}

// Client 实现 model.Model 与 model.StreamingModel。
type Client struct {
	apiKey  string
	baseURL string
	model   string
	maxTok  int
	http    *http.Client
}

// New 创建一个 OpenAI 兼容客户端。
func New(cfg Config) *Client {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{apiKey: apiKey, baseURL: baseURL, model: cfg.Model, maxTok: cfg.MaxTokens, http: hc}
}

// --- wire 类型 ---

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wireToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireResponse struct {
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// --- 转换 ---

func toWireMessages(msgs []core.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			var wc wireToolCall
			wc.ID = tc.ID
			wc.Type = "function"
			wc.Function.Name = tc.Name
			wc.Function.Arguments = string(tc.Arguments)
			wm.ToolCalls = append(wm.ToolCalls, wc)
		}
		out = append(out, wm)
	}
	return out
}

func fromWireMessage(m wireMessage) core.Message {
	out := core.Message{
		Role:       core.Role(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	for _, tc := range m.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, core.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(args),
		})
	}
	return out
}

func toWireTools(tools []tool.ToolInfo) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		wt.Function.Parameters = params
		out = append(out, wt)
	}
	return out
}

// --- 请求 ---

func (c *Client) buildRequest(req model.Request, stream bool) wireRequest {
	model := req.Model
	if model == "" {
		model = c.model
	}
	maxTokens := req.MaxTokens
	if maxTokens == nil && c.maxTok > 0 {
		maxTokens = &c.maxTok
	}
	return wireRequest{
		Model:       model,
		Messages:    toWireMessages(req.Messages),
		Tools:       toWireTools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   maxTokens,
		Stream:      stream,
	}
}

func (c *Client) newRequest(ctx context.Context, body io.Reader) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return httpReq, nil
}

// Generate 实现 model.Model。
func (c *Client) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(c.buildRequest(req, false)); err != nil {
		return model.Response{}, fmt.Errorf("openai: encode request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, &buf)
	if err != nil {
		return model.Response{}, fmt.Errorf("openai: build request: %w", err)
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return model.Response{}, fmt.Errorf("openai: do request: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return model.Response{}, fmt.Errorf("openai: read response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return model.Response{}, fmt.Errorf("openai: http %d: %s", httpResp.StatusCode, truncate(string(raw), 512))
	}

	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return model.Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if wr.Error != nil {
		return model.Response{}, fmt.Errorf("openai: %s", wr.Error.Message)
	}
	if len(wr.Choices) == 0 {
		return model.Response{}, fmt.Errorf("openai: response has no choices")
	}

	choice := wr.Choices[0]
	return model.Response{
		Message: fromWireMessage(choice.Message),
		Usage: core.Usage{
			PromptTokens:     wr.Usage.PromptTokens,
			CompletionTokens: wr.Usage.CompletionTokens,
			TotalTokens:      wr.Usage.TotalTokens,
		},
		FinishReason: choice.FinishReason,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
