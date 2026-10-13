package audit

import (
	"context"
	"fmt"
	"testing"
)

func TestRingOrderAcrossCapacityChanges(t *testing.T) {
	a := NewMemoryAudit().WithMaxRecords(3)
	ctx := context.Background()
	for i := range 8 {
		if err := a.Record(ctx, AuditRecord{Component: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	a.WithMaxRecords(2)
	records, _ := a.Query(ctx, AuditQuery{})
	if len(records) != 2 || records[0].Component != "6" || records[1].Component != "7" {
		t.Fatalf("records=%+v", records)
	}
	a.WithMaxRecords(0)
	a.Record(ctx, AuditRecord{Component: "8"})
	records, _ = a.Query(ctx, AuditQuery{})
	if len(records) != 3 || records[0].Component != "6" || records[2].Component != "8" {
		t.Fatalf("records=%+v", records)
	}
}

func TestRecordCopiesInputDetail(t *testing.T) {
	a := NewMemoryAudit()
	detail := map[string]any{"status": "original"}
	a.Record(context.Background(), AuditRecord{Detail: detail})
	detail["status"] = "mutated"
	records, _ := a.Query(context.Background(), AuditQuery{})
	if records[0].Detail["status"] != "original" {
		t.Fatal("input aliases audit")
	}
}
