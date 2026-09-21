package tinyagent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryAudit_RecordAndQuery(t *testing.T) {
	a := NewMemoryAudit()
	ctx := context.Background()

	if err := a.Record(ctx, AuditRecord{SessionID: "s1", RunID: "r1", Kind: AuditToolCall, Component: "tool:x"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, AuditRecord{SessionID: "s1", RunID: "r1", Kind: AuditModelCall}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, AuditRecord{SessionID: "s2", RunID: "r2", Kind: AuditToolCall}); err != nil {
		t.Fatal(err)
	}

	if a.Len() != 3 {
		t.Errorf("len = %d, want 3", a.Len())
	}

	got, err := a.Query(ctx, AuditQuery{SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("query by session = %d, want 2", len(got))
	}

	got, err = a.Query(ctx, AuditQuery{Kind: AuditToolCall})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("query by kind = %d, want 2", len(got))
	}

	got, err = a.Query(ctx, AuditQuery{SessionID: "s1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("query with limit = %d, want 1", len(got))
	}

	if got[0].Time.IsZero() {
		t.Error("Record should stamp Time when zero")
	}
}

func TestMemoryAudit_SinceFilter(t *testing.T) {
	a := NewMemoryAudit()
	ctx := context.Background()
	old := time.Now().Add(-time.Hour)

	if err := a.Record(ctx, AuditRecord{Time: old, Kind: AuditRunStart}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, AuditRecord{Kind: AuditRunEnd}); err != nil {
		t.Fatal(err)
	}

	got, err := a.Query(ctx, AuditQuery{Since: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != AuditRunEnd {
		t.Errorf("since filter = %+v", got)
	}
}

func TestMemoryAudit_MaxRecords(t *testing.T) {
	a := NewMemoryAudit().WithMaxRecords(3)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := a.Record(ctx, AuditRecord{Kind: AuditRunStart, Component: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	if a.Len() != 3 {
		t.Errorf("len = %d, want 3", a.Len())
	}
	got, err := a.Query(ctx, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Component != "c" {
		t.Errorf("oldest records should be dropped, got %q", got[0].Component)
	}
}

func TestMemoryAudit_DetailIsolated(t *testing.T) {
	a := NewMemoryAudit()
	ctx := context.Background()
	if err := a.Record(ctx, AuditRecord{Kind: AuditToolCall, Detail: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}

	got, err := a.Query(ctx, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got[0].Detail["k"] = "mutated"

	again, err := a.Query(ctx, AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Detail["k"] != "v" {
		t.Error("Detail leaked from query result")
	}
}

func TestMemoryAudit_Concurrent(t *testing.T) {
	a := NewMemoryAudit()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = a.Record(ctx, AuditRecord{Kind: AuditToolCall})
				_, _ = a.Query(ctx, AuditQuery{})
			}
		}()
	}
	wg.Wait()

	if a.Len() != 1000 {
		t.Errorf("len = %d, want 1000", a.Len())
	}
}

func TestMemoryAudit_ContextCanceled(t *testing.T) {
	a := NewMemoryAudit()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.Record(ctx, AuditRecord{}); err != context.Canceled {
		t.Errorf("Record = %v, want context.Canceled", err)
	}
	if _, err := a.Query(ctx, AuditQuery{}); err != context.Canceled {
		t.Errorf("Query = %v, want context.Canceled", err)
	}
}
