package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"github.com/pcc-258/tinyagent/core"
)

func TestMemoryStore_AppendAndLoad(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()

	if _, err := st.Load(ctx, "s1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Load missing = %v, want core.ErrNotFound", err)
	}

	if err := st.Append(ctx, "s1", core.Message{Role: core.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s1", core.Message{Role: core.RoleAssistant, Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	snap, err := st.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(snap.Messages))
	}
	if snap.Messages[0].Content != "hi" || snap.Messages[1].Content != "hello" {
		t.Errorf("messages = %+v", snap.Messages)
	}
}

func TestMemoryStore_Replace(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	if err := st.Append(ctx, "s1",
		core.Message{Content: "a"}, core.Message{Content: "b"}, core.Message{Content: "c"}); err != nil {
		t.Fatal(err)
	}

	if err := st.Replace(ctx, "s1", []core.Message{{Content: "summary"}}); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 1 || snap.Messages[0].Content != "summary" {
		t.Errorf("messages = %+v", snap.Messages)
	}
}

// TestMemoryStore_SnapshotIsolation 验证返回的快照与内部状态完全隔离。
func TestMemoryStore_SnapshotIsolation(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	if err := st.Append(ctx, "s1", core.Message{
		Role:      core.RoleAssistant,
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "t", Arguments: []byte(`{"a":1}`)}},
		Meta:      map[string]any{"k": "v"},
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := st.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	snap.Messages[0].Content = "mutated"
	snap.Messages[0].ToolCalls[0].Arguments[0] = 'X'
	snap.Messages[0].Meta["k"] = "mutated"

	again, err := st.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Messages[0].Content == "mutated" {
		t.Error("Content leaked from snapshot")
	}
	if string(again.Messages[0].ToolCalls[0].Arguments) != `{"a":1}` {
		t.Errorf("Arguments leaked: %s", again.Messages[0].ToolCalls[0].Arguments)
	}
	if again.Messages[0].Meta["k"] != "v" {
		t.Error("Meta leaked from snapshot")
	}
}

func TestMemoryStore_ListAndDelete(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	if err := st.Append(ctx, "a", core.Message{Content: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "b", core.Message{Content: "2"}); err != nil {
		t.Fatal(err)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("list len = %d, want 2", len(list))
	}

	if err := st.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "a"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("second delete = %v, want core.ErrNotFound", err)
	}
	list, err = st.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "b" {
		t.Errorf("list after delete = %+v", list)
	}
}

func TestMemoryStore_ContextCanceled(t *testing.T) {
	st := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := st.Load(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Load = %v, want context.Canceled", err)
	}
	if err := st.Append(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Append = %v, want context.Canceled", err)
	}
	if err := st.Replace(ctx, "s1", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Replace = %v, want context.Canceled", err)
	}
	if _, err := st.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List = %v, want context.Canceled", err)
	}
	if err := st.Delete(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete = %v, want context.Canceled", err)
	}
}

func TestMemoryStore_Concurrent(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = st.Append(ctx, "shared", core.Message{Content: "x"})
				_, _ = st.Load(ctx, "shared")
				_, _ = st.List(ctx)
			}
		}()
	}
	wg.Wait()

	snap, err := st.Load(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 20*50 {
		t.Errorf("messages = %d, want %d", len(snap.Messages), 20*50)
	}
}

func TestSession_SnapshotIsolation(t *testing.T) {
	s := NewSession("s1")
	s.Append(core.Message{Role: core.RoleUser, Content: "hi", Meta: map[string]any{"k": "v"}})

	snap := s.Snapshot()
	snap.Messages[0].Content = "mutated"
	snap.Meta["new"] = "x"

	if got := s.Messages(); got[0].Content != "hi" {
		t.Errorf("Content leaked: %q", got[0].Content)
	}
	if _, ok := s.Meta["new"]; ok {
		t.Error("Meta leaked from snapshot")
	}
}

func TestSession_BeginRunExclusive(t *testing.T) {
	s := NewSession("s1")
	if !s.TryAcquire() {
		t.Fatal("first TryAcquire should succeed")
	}
	if s.TryAcquire() {
		t.Fatal("second TryAcquire should fail while running")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("TryAcquire after release should succeed")
	}
	s.Release()
}
