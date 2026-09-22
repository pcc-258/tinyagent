package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/model/openai"
)

func newAgentWithServer(t *testing.T, srv *httptest.Server) *tinyagent.Agent {
	t.Helper()
	ag, err := tinyagent.New(tinyagent.Config{
		Model: openai.New(openai.Config{
			APIKey:  "test-key",
			BaseURL: srv.URL,
			Model:   "test-model",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

// TestAgentEndToEndModelHTTPError verifies a model-side HTTP failure surfaces as
// a run error instead of hanging or panicking.
func TestAgentEndToEndModelHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	ag := newAgentWithServer(t, srv)
	_, err := collectRun(ag.Run(context.Background(), "robust", "hi"))
	if err == nil {
		t.Fatal("expected run error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("err = %v, want http 500", err)
	}
}

// TestAgentEndToEndMalformedStream verifies malformed SSE data does not crash
// the agent and is reported as a model error.
func TestAgentEndToEndMalformedStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		io.WriteString(w, "data: {not-json\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	ag := newAgentWithServer(t, srv)
	_, err := collectRun(ag.Run(context.Background(), "robust", "hi"))
	if err == nil {
		t.Fatal("expected run error on malformed stream")
	}
	if !strings.Contains(err.Error(), "decode stream chunk") {
		t.Errorf("err = %v, want decode stream chunk", err)
	}
}

// TestAgentEndToEndLengthTruncation verifies an empty response truncated by
// finish_reason=length is treated as a model error, not a silent end.
func TestAgentEndToEndLengthTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		flusher.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	ag := newAgentWithServer(t, srv)
	_, err := collectRun(ag.Run(context.Background(), "robust", "hi"))
	if err == nil {
		t.Fatal("expected run error on truncated output")
	}
	if !strings.Contains(err.Error(), "output truncated") {
		t.Errorf("err = %v, want output truncated", err)
	}
}
