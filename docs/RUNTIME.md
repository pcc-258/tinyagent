# Runtime control and state ownership

TinyAgent remains an in-process library with a standard-library-only core. The
runtime changes below are additive except for stricter correctness checks:
unsatisfiable context budgets fail, persistence failures reach callers, and
truncated model output is an error rather than a successful finish.

## Execution and persistence

`SequentialTools: true` executes tools in model-request order. Otherwise a worker
pool runs up to `ToolConcurrency` tools (default 8), while result events retain
request order. Use sequential mode when calls have side effects that depend on
one another; ordered result events do not imply ordered execution in parallel mode.

A complete tool batch is appended to the session before its result events are
emitted. A consumer breaking at a tool-call event leaves explicit “not executed”
results; breaking at a result event preserves all executed results. Restored
history with missing results receives an “execution outcome unavailable” response
before the next model call. This repairs message pairing without rerunning a tool
or claiming that an external side effect was rolled back.

The facade owns a per-session run lease through Store writeback. Another run of
that session returns `core.ErrSessionBusy` until the write completes. Different
sessions can load and run independently. `EventRunEnd` is delivered after
writeback, and `Chat` returns persistence failures. If a consumer has already
broken iteration, writeback still runs and a failure is logged: it is no longer
legal to yield an error into the stopped iterator.

`PersistTimeout` defaults to 10 seconds and bounds writeback using a context
independent of run cancellation. A Store must respect that context. These leases
coordinate calls through one Agent; they are not a distributed lock across
multiple Agents or processes sharing a Store. Such a Store needs its own
version/transaction or ownership mechanism.

Session message ingress and snapshots copy message-owned maps, tool-call slices,
and argument bytes. Nested objects inside `Meta` remain application-owned.
Applications must synchronize direct access to exported Session metadata and
must not mutate the session's history during an active run.

## Panic policies

The default runner guards model iteration, tools, context processing, counters,
hooks and stop conditions. A recovered Hook panic emits a panic event, an audit
record and a log, then skips that Hook. `PanicFailRun` terminates the run instead;
`PanicPropagate` intentionally propagates component panics. Recovery cannot undo
mutations a Hook made before panicking. Consumer callbacks are application code
and their panics propagate normally.

## Stop conditions

```go
ag, err := tinyagent.New(tinyagent.Config{
    Model: mdl,
    Tools: tools,
    StopConditions: []runner.StopCondition{
        runner.StepCountIs(8),
        runner.MaxTokensUsed(20000),
        runner.HasToolCall("finish"),
    },
})
```

Conditions are OR-ed after a complete model/tool step has been committed. They
never extend `MaxIterations` or a context deadline. `HasToolCall` tests the call's
name, not whether its business operation succeeded. Custom conditions receive a
copied assistant message and accumulated usage. Token conditions use provider
usage and stop after a completed step; they are not a pre-call spending guarantee.
A natural assistant answer still ends the run without requiring a condition.

The default budget guidance and final-answer turn remain in place. The last
iteration disables tools, as before. Conditions give applications additional
stopping rules without replacing the whole Runner.

## Cooperative cancellation

```go
ctx, cancelRun := runner.WithCancel(parent)
// Another goroutine or a UI callback can request a safe stop:
cancelRun(runner.CancelAfterTools, 5*time.Second)
for ev, err := range ag.Run(ctx, "session", input) {
    // Handle events; cancellation is reported as context.Canceled.
    _ = ev
    _ = err
}
```

`CancelImmediate` cancels the run context; `CancelAfterModel` waits for the current
model call and does not dispatch its tools; `CancelAfterTools` lets the current
batch finish before stopping. Safe requests escalate to immediate cancellation
after the supplied timeout (default 5 seconds). Parent cancellation/deadlines
remain immediate. Tools and other callbacks must honor context: Go cannot
forcibly stop a callback that ignores it.

The control context is for one consumed run and closes when the default Runner
finishes. If the iterator will never be consumed, call `CancelImmediate` to close
it. Custom Runners must implement their own safe-point semantics. To continue a
stopped conversation, run that session again; committed tools are represented by
results, so the kernel does not replay them. This is conversation continuation,
not arbitrary process/sandbox checkpoint restoration.

## Context budget, offload and summaries

`ContextBudget` includes messages, tools and the actual generated system prompt.
`OutputReserve` subtracts output capacity from the input budget and sets the
request's output-token cap. Set it to match the model's desired output limit.
Hooks that expand a request are followed by a final count. The default counter is
an estimate; supply the model's tokenizer through `Counter` for accurate bounds.
If the retained context still cannot fit, the run returns
`core.ErrContextBudget` instead of sending an oversized request.

The default context manager offloads large tool results and then truncates older
history without splitting assistant/tool groups. Its preferred recent-message
retention can shrink further when necessary to fit the budget; it does not
silently cut the content of a single oversized user message.

The default Agent wires `read_offloaded` to the same Offloader. Its byte-range
reads default to 2048 bytes, cap at 4096 bytes, and return `next_offset`; UTF-8
boundaries are preserved. Reads are scoped to references in the conversation's
message metadata, including references retained by SummaryStrategy. Custom
Runners must use `ctxmgr.WithOffloadReferences` when invoking this tool.
`read_offloaded` is reserved when the Agent uses its default context manager.

Configure `Config.Offloader` for durable storage. The default MemoryOffloader is
process-local, caps at 1024 references / 64 MiB, and returns a capacity error
rather than evicting live data. Its options support TTL, and `Delete` releases
references known to be unused. Negative capacity values disable that cap; TTL is
disabled by default. Persisted references require the same durable Offloader
across restart. Expired/deleted references return `core.ErrNotFound`.

Summarization is optional and does not add a model call by default:

```go
offloader := ctxmgr.NewMemoryOffloader()
counter := ctxmgr.NewSimpleCounter()
manager := &ctxmgr.DefaultContextManager{
    Counter: counter,
    Strategies: []ctxmgr.ContextStrategy{
        &ctxmgr.OffloadStrategy{Offloader: offloader},
        &ctxmgr.SummaryStrategy{KeepRecent: 12, Summarize: summarizeHistory},
        &ctxmgr.TruncateStrategy{Counter: counter},
    },
}
readTool, err := ctxmgr.NewReadOffloadedTool(offloader)
// Check err, then include readTool in Config.Tools and manager in Config.Context.
```

The application supplies `summarizeHistory(context.Context, []core.Message)
(string, error)` and controls its model, cost and retries. SummaryStrategy keeps
recent tool groups intact and carries prior offload capabilities into summary
metadata. The manager recounts after strategies and checks the final budget.
Custom ContextManagers own their own strategy/retention semantics; their returned
message state is committed by the default Runner, even without action events.

## Resource lifecycle and provider compatibility

`MaxCachedSessions` is an optional hard bound (default unlimited).
`UnloadSession(ctx, id)` writes an idle session and removes it from the Agent
cache without deleting its Store record. It rejects active runs and loads.
Previously returned Session handles must not be used after successful unload.
The default MemoryStore itself retains records until `Store.Delete`; use a durable
Store when unloading needs to reclaim process memory rather than only cache copies.

The Agent's default MemoryAudit retains the latest 10000 records with a ring
buffer. Supply a custom Audit or `NewMemoryAudit().WithMaxRecords(n)` to change
retention; n <= 0 disables the bound. Input/query detail maps are copied.

OpenAI streaming requests now include `stream_options.include_usage`, and the
adapter accepts usage-only chunks with empty choices. For compatible endpoints
that reject that option, set `openai.Config.DisableStreamUsage: true`.

## Design references

Mechanisms were informed by these projects; TinyAgent adds no dependency on them:

- [Fantasy stop conditions and step preparation](https://github.com/charmbracelet/fantasy/blob/main/agent.go)
- [Eino missing tool-result repair](https://www.cloudwego.io/zh/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_patchtoolcalls/)
- [Eino cancellation safe points](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_cancel_and_turnloop_quickstart/)
- [Eino result offload](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_toolreduction/)
- [ADK Go commit before yield](https://github.com/google/adk-go/blob/main/runner/runner.go)
- [Genkit explicit interruption and continuation](https://genkit.dev/docs/go/interrupts/)
