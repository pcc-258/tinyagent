package openai

import (
	"context"
	"os"
	"testing"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
)

// TestRealAPI 真实调用一次模型端点，验证 wire 格式与端到端链路。
//
// 仅在设置 TINYAGENT_REAL_API_KEY 时运行，默认跳过，
// 以免在无凭据环境或 CI 中产生费用。
func TestRealAPI(t *testing.T) {
	key := os.Getenv("TINYAGENT_REAL_API_KEY")
	if key == "" {
		t.Skip("set TINYAGENT_REAL_API_KEY to run the real API test")
	}

	client := New(Config{
		APIKey:  key,
		BaseURL: os.Getenv("TINYAGENT_REAL_BASE_URL"),
		Model:   os.Getenv("TINYAGENT_REAL_MODEL"),
	})
	ctx := context.Background()

	resp, err := client.Generate(ctx, model.Request{
		Messages: []core.Message{{Role: core.RoleUser, Content: "Reply with exactly one word: pong"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.Content == "" {
		t.Error("Generate returned empty content")
	}
	t.Logf("generate -> %q (usage: prompt=%d completion=%d)", resp.Message.Content, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)

	var streamed string
	for chunk, err := range client.Stream(ctx, model.Request{
		Messages: []core.Message{{Role: core.RoleUser, Content: "Count from 1 to 3, digits only."}},
	}) {
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		streamed += chunk.TextDelta
	}
	if streamed == "" {
		t.Error("Stream returned empty content")
	}
	t.Logf("stream -> %q", streamed)
}
