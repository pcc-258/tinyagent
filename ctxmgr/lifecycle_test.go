package ctxmgr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pcc-258/tinyagent/core"
)

func TestUnsatisfiableBudgetReturnsError(t *testing.T) {
	m := NewDefaultContextManager(NewSimpleCounter())
	for _, msgs := range [][]core.Message{{{Role: core.RoleUser, Content: strings.Repeat("x", 4000)}}, {{Role: core.RoleUser, Content: "hi"}}} {
		system := ""
		if len(msgs[0].Content) < 100 {
			system = strings.Repeat("x", 4000)
		}
		_, _, err := m.Prepare(context.Background(), msgs, nil, system, 100)
		if !errors.Is(err, core.ErrContextBudget) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestTrimmingFitsBudgetAndPreservesGroups(t *testing.T) {
	m := NewDefaultContextManager(NewSimpleCounter())
	msgs := make([]core.Message, 0)
	for range 30 {
		msgs = append(msgs, core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 40)})
	}
	msgs = append(msgs, core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1"}, {ID: "c2"}}}, core.Message{Role: core.RoleTool, ToolCallID: "c1", Content: "a"}, core.Message{Role: core.RoleTool, ToolCallID: "c2", Content: "b"})
	out, _, err := m.Prepare(context.Background(), msgs, nil, "system", 100)
	if err != nil {
		t.Fatal(err)
	}
	count, _ := m.Counter.Count(context.Background(), CountRequest{Messages: out, System: "system"})
	if count.Total > 100 {
		t.Fatalf("count=%d", count.Total)
	}
	if _, missing := core.RepairToolCalls(out, "missing"); missing != 0 {
		t.Fatal("trimming broke tool group")
	}
	if out[0].Role == core.RoleTool {
		t.Fatal("orphaned tool result")
	}
}

func TestReadOffloadedRangesAndUTF8(t *testing.T) {
	off := NewMemoryOffloader()
	content := strings.Repeat("中文abc", 500)
	ref, err := off.Offload(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	tl, err := NewReadOffloadedTool(off)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOffloadReferences(context.Background(), []core.Message{{Meta: map[string]any{offloadRefKey: ref}}})
	res, err := tl.Run(ctx, json.RawMessage(`{"ref":"`+ref+`","limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "next_offset=9") || !utf8.ValidString(res.Content) {
		t.Fatalf("result=%q", res.Content)
	}
	_, err = tl.Run(ctx, json.RawMessage(`{"ref":"`+ref+`","offset":1}`))
	if err == nil {
		t.Fatal("accepted middle of UTF-8 character")
	}
}

func TestOffloaderCapacityDoesNotEvictLiveReferences(t *testing.T) {
	off := NewMemoryOffloaderWithOptions(MemoryOffloaderOptions{MaxItems: 1, MaxBytes: 10})
	ref, err := off.Offload(context.Background(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.Offload(context.Background(), "other"); err == nil {
		t.Fatal("capacity not enforced")
	}
	got, err := off.Retrieve(context.Background(), ref)
	if err != nil || got != "original" {
		t.Fatalf("live reference evicted: %q %v", got, err)
	}
	if err := off.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := off.Offload(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
}

func TestOffloaderExpiry(t *testing.T) {
	off := NewMemoryOffloaderWithOptions(MemoryOffloaderOptions{TTL: time.Hour})
	ref, _ := off.Offload(context.Background(), "old")
	// Move only the fixture expiry into the past; avoid a wall-clock sleep.
	off.mu.Lock()
	item := off.items[ref]
	item.expires = time.Now().Add(-time.Second)
	off.items[ref] = item
	off.mu.Unlock()
	if _, err := off.Retrieve(context.Background(), ref); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if off.bytes != 0 {
		t.Fatalf("expired bytes retained=%d", off.bytes)
	}
}

func TestSummaryPreservesRecentToolGroup(t *testing.T) {
	msgs := []core.Message{{Role: core.RoleUser, Content: "old request"}, {Role: core.RoleAssistant, Content: "old reply"}, {Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c"}}}, {Role: core.RoleTool, ToolCallID: "c", Content: "result"}, {Role: core.RoleUser, Content: "latest"}}
	s := &SummaryStrategy{KeepRecent: 2, Summarize: func(_ context.Context, old []core.Message) (string, error) {
		if len(old) != 2 {
			t.Fatalf("summary boundary=%+v", old)
		}
		return "old context", nil
	}}
	out, action, err := s.Apply(context.Background(), msgs, 100)
	if err != nil || action == nil {
		t.Fatalf("action=%+v err=%v", action, err)
	}
	if len(out) != 4 || out[1].Role != core.RoleAssistant {
		t.Fatalf("out=%+v", out)
	}
	if _, n := core.RepairToolCalls(out, "missing"); n != 0 {
		t.Fatal("summary broke tool group")
	}
}

func TestReadOffloadedRejectsOtherConversation(t *testing.T) {
	off := NewMemoryOffloader()
	ref, _ := off.Offload(context.Background(), "private result")
	tl, _ := NewReadOffloadedTool(off)
	ctx := WithOffloadReferences(context.Background(), nil)
	if _, err := tl.Run(ctx, json.RawMessage(`{"ref":"`+ref+`"}`)); err == nil {
		t.Fatal("cross-conversation reference accepted")
	}
}
