package core

// RepairToolCalls inserts missing responses immediately after their assistant
// message and existing results. It never claims a missing result means success
// or cancellation: callers supply the known execution state in reason.
func RepairToolCalls(msgs []Message, reason string) ([]Message, int) {
	out := make([]Message, 0, len(msgs))
	repaired := 0
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for i+1 < len(msgs) && msgs[i+1].Role == RoleTool {
			i++
			out = append(out, msgs[i])
			seen[msgs[i].ToolCallID] = true
		}
		for _, call := range m.ToolCalls {
			if !seen[call.ID] {
				out = append(out, MessageFromToolResult(ToolResult{ToolCallID: call.ID, Error: reason}))
				seen[call.ID] = true
				repaired++
			}
		}
	}
	return out, repaired
}
