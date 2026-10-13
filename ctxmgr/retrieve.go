package ctxmgr

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/tool"
)

const offloadRefKey = "tinyagent.offloaded_ref"
const offloadRefsKey = "tinyagent.offloaded_refs"

type offloadScopeKey struct{}

// WithOffloadReferences limits reads to references present in the current
// conversation. The default runner supplies this context automatically. Custom
// runners using read_offloaded must also supply a scope; refs are capabilities.
func WithOffloadReferences(ctx context.Context, msgs []core.Message) context.Context {
	return context.WithValue(ctx, offloadScopeKey{}, offloadReferences(msgs))
}

func offloadReferences(msgs []core.Message) map[string]bool {
	refs := make(map[string]bool)
	for _, m := range msgs {
		if ref, ok := m.Meta[offloadRefKey].(string); ok {
			refs[ref] = true
		}
		switch list := m.Meta[offloadRefsKey].(type) {
		case []string:
			for _, ref := range list {
				refs[ref] = true
			}
		case []any:
			for _, value := range list {
				if ref, ok := value.(string); ok {
					refs[ref] = true
				}
			}
		}
	}
	return refs
}

// ReadOffloadedArgs reads a bounded byte range, so retrieving a large result
// does not immediately overflow context again. Offsets must be UTF-8 boundaries.
type ReadOffloadedArgs struct {
	Ref    string `json:"ref" desc:"Offloaded reference from the message"`
	Offset int    `json:"offset,omitempty" desc:"Byte offset, default 0; use next_offset to continue"`
	Limit  int    `json:"limit,omitempty" desc:"Maximum bytes to read, default 2048, maximum 4096"`
}

func NewReadOffloadedTool(offloader Offloader) (tool.Tool, error) {
	if offloader == nil {
		return nil, fmt.Errorf("offloader is required")
	}
	return tool.NewFuncTool("read_offloaded", "Read a bounded range of an offloaded tool result", func(ctx context.Context, args ReadOffloadedArgs) (core.ToolResult, error) {
		refs, _ := ctx.Value(offloadScopeKey{}).(map[string]bool)
		if !refs[args.Ref] {
			return core.ToolResult{}, fmt.Errorf("offloaded reference is not available in this conversation")
		}
		content, err := offloader.Retrieve(ctx, args.Ref)
		if err != nil {
			return core.ToolResult{}, err
		}
		if args.Offset < 0 || args.Offset > len(content) {
			return core.ToolResult{}, fmt.Errorf("offset out of range")
		}
		if args.Offset < len(content) && !utf8.RuneStart(content[args.Offset]) {
			return core.ToolResult{}, fmt.Errorf("offset must be a UTF-8 boundary")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 2048
		}
		if limit > 4096 {
			limit = 4096
		}
		end := min(len(content), args.Offset+limit)
		for end > args.Offset && end < len(content) && !utf8.RuneStart(content[end]) {
			end--
		}
		if end == args.Offset && end < len(content) {
			return core.ToolResult{}, fmt.Errorf("limit too small for next UTF-8 character")
		}
		return core.ToolResult{Content: fmt.Sprintf("bytes=%d:%d total_bytes=%d next_offset=%d\n%s", args.Offset, end, len(content), end, content[args.Offset:end])}, nil
	})
}
