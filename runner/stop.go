package runner

import (
	"time"

	"github.com/pcc-258/tinyagent/core"
)

// StepResult describes a committed model/tool step. Stop conditions cannot
// extend the hard iteration or context deadline limits.
type StepResult struct {
	Iteration int
	Elapsed   time.Duration
	Usage     core.Usage
	Message   core.Message
}

type StopCondition func(StepResult) bool

func StepCountIs(n int) StopCondition {
	return func(s StepResult) bool { return n > 0 && s.Iteration >= n }
}

func MaxTokensUsed(n int) StopCondition {
	return func(s StepResult) bool { return n > 0 && s.Usage.TotalTokens >= n }
}

func HasToolCall(name string) StopCondition {
	return func(s StepResult) bool {
		for _, c := range s.Message.ToolCalls {
			if c.Name == name {
				return true
			}
		}
		return false
	}
}
