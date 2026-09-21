package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
)

type wireStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Stream 实现 model.StreamingModel。
//
// 工具调用的 name 与 id 只在首片出现，arguments 分多片到达；
// 本方法只做透传，聚合由 model.ToolCallAccumulator 完成。
func (c *Client) Stream(ctx context.Context, req model.Request) iter.Seq2[model.Chunk, error] {
	return func(yield func(model.Chunk, error) bool) {
		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(c.buildRequest(req, true)); err != nil {
			yield(model.Chunk{}, fmt.Errorf("openai: encode request: %w", err))
			return
		}

		httpReq, err := c.newRequest(ctx, &body)
		if err != nil {
			yield(model.Chunk{}, fmt.Errorf("openai: build request: %w", err))
			return
		}
		httpReq.Header.Set("Accept", "text/event-stream")

		httpResp, err := c.http.Do(httpReq)
		if err != nil {
			yield(model.Chunk{}, fmt.Errorf("openai: do request: %w", err))
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(httpResp.Body)
			yield(model.Chunk{}, fmt.Errorf("openai: http %d: %s", httpResp.StatusCode, truncate(string(raw), 512)))
			return
		}

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

		for scanner.Scan() {
			payload, ok := sseData(scanner.Text())
			if !ok {
				continue
			}
			if payload == "[DONE]" {
				return
			}

			var sc wireStreamChunk
			if err := json.Unmarshal([]byte(payload), &sc); err != nil {
				if !yield(model.Chunk{}, fmt.Errorf("openai: decode stream chunk: %w", err)) {
					return
				}
				continue
			}
			if sc.Error != nil {
				yield(model.Chunk{}, fmt.Errorf("openai: %s", sc.Error.Message))
				return
			}
			if len(sc.Choices) == 0 {
				continue
			}

			delta := sc.Choices[0].Delta

			if delta.Content != "" {
				if !yield(model.Chunk{TextDelta: delta.Content}, nil) {
					return
				}
			}
			for _, tc := range delta.ToolCalls {
				if !yield(model.Chunk{ToolCall: &model.ToolCallDelta{
					Index:          tc.Index,
					ID:             tc.ID,
					Name:           tc.Function.Name,
					ArgumentsDelta: tc.Function.Arguments,
				}}, nil) {
					return
				}
			}
			if sc.Usage != nil {
				if !yield(model.Chunk{Usage: &core.Usage{
					PromptTokens:     sc.Usage.PromptTokens,
					CompletionTokens: sc.Usage.CompletionTokens,
					TotalTokens:      sc.Usage.TotalTokens,
				}}, nil) {
					return
				}
			}
			if fr := sc.Choices[0].FinishReason; fr != "" {
				if !yield(model.Chunk{FinishReason: fr}, nil) {
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			yield(model.Chunk{}, fmt.Errorf("openai: read stream: %w", err))
		}
	}
}

// sseData 从一行 SSE 文本中提取 data 负载。
func sseData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return "", false
	}
	return payload, true
}
