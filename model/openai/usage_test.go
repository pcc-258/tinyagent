package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pcc-258/tinyagent/model"
)

type usageTransport func(*http.Request) (*http.Response, error)

func (f usageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUsageOnlyTailAndRequestOption(t *testing.T) {
	for _, disable := range []bool{false, true} {
		c := New(Config{DisableStreamUsage: disable, HTTPClient: &http.Client{Transport: usageTransport(func(r *http.Request) (*http.Response, error) {
			var wr wireRequest
			if err := json.NewDecoder(r.Body).Decode(&wr); err != nil {
				t.Fatal(err)
			}
			if disable && wr.StreamOptions != nil {
				t.Fatal("option should be omitted")
			}
			if !disable && (wr.StreamOptions == nil || !wr.StreamOptions.IncludeUsage) {
				t.Fatal("usage not requested")
			}
			data := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data))}, nil
		})}})
		total := 0
		for chunk, err := range c.Stream(context.Background(), model.Request{}) {
			if err != nil {
				t.Fatal(err)
			}
			if chunk.Usage != nil {
				total = chunk.Usage.TotalTokens
			}
		}
		if total != 12 {
			t.Fatalf("usage=%d want 12", total)
		}
	}
}
