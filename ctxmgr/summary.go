package ctxmgr

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pcc-258/tinyagent/core"
)

// SummaryStrategy is opt-in: the application supplies a summary model/function
// and controls its cost and retry policy. Recent tool-call groups stay intact.
type SummaryStrategy struct {
	KeepRecent int
	Summarize  func(context.Context, []core.Message) (string, error)
}

func (*SummaryStrategy) Name() string { return "summarize" }

func (s *SummaryStrategy) Apply(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, *core.ContextAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s.Summarize == nil {
		return msgs, nil, nil
	}
	keep := s.KeepRecent
	if keep <= 0 {
		keep = 20
	}
	if len(msgs) <= keep {
		return msgs, nil, nil
	}
	start := safeCutPoint(msgs, len(msgs)-keep)
	if start <= 1 {
		return msgs, nil, nil
	}
	summary, err := s.Summarize(ctx, core.CloneMessages(msgs[:start]))
	if err != nil {
		return nil, nil, fmt.Errorf("summarize history: %w", err)
	}
	if strings.TrimSpace(summary) == "" {
		return nil, nil, fmt.Errorf("summarizer returned empty summary")
	}
	out := []core.Message{{Role: core.RoleUser, Content: "Summary of earlier conversation:\n" + summary}}
	refs := make([]string, 0)
	for ref := range offloadReferences(msgs[:start]) {
		refs = append(refs, ref)
	}
	if len(refs) > 0 {
		sort.Strings(refs)
		out[0].Meta = map[string]any{offloadRefsKey: refs}
	}
	out = append(out, core.CloneMessages(msgs[start:])...)
	return out, &core.ContextAction{Strategy: s.Name(), Detail: map[string]any{"summarized_messages": start}}, nil
}
