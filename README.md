# TinyAgent

> 进程内嵌的 Go agent 内核 —— 几行代码，让你的程序拥有一个完整的 agent。

**状态：v0.1 核心已可用。** 进度见 [构建计划](docs/BUILD_PLAN.md)。

---

## 这是什么

TinyAgent 是一个可以直接 `import` 进你程序的 agent 库。

不需要启动 CLI，不需要 sidecar，不需要和外部 agent 进程通信。就是函数调用。

## 快速开始

```go
model := openai.New(openai.Config{Model: "deepseek-chat"})

ag, _ := tinyagent.New(tinyagent.Config{Model: model})
reply, _ := ag.Chat(ctx, "session-1", "用一句话介绍 Go")
```

完整示例见 [`examples/`](examples/)：

| 示例 | 说明 | 需要 API |
|---|---|---|
| [`offline`](examples/offline) | 工具 + Hook + 事件流 + 审计全流程 | 否 |
| [`panicsafe`](examples/panicsafe) | 崩溃隔离：工具 panic 后宿主存活 | 否 |
| [`customrunner`](examples/customrunner) | 整体替换 agent loop | 否 |
| [`quickstart`](examples/quickstart) | 最小接入 | 是 |
| [`tools`](examples/tools) | 工具注册与事件消费 | 是 |

```bash
go run ./examples/offline        # 无需任何凭据
go run ./examples/panicsafe
go run ./examples/customrunner
```

## 为什么

现有的 Go agent 方案，要么默认给你一个 CLI（`adk-go`），要么依赖很重，要么需要你手写工具循环（`langchaingo`）。TinyAgent 的取向不同：

| 主张 | 含义 |
|---|---|
| **进程内嵌** | 无 CLI、无 sidecar、无 IPC |
| **崩溃不带崩主程序** | 接入方代码 panic、库组件 panic，一律捕获且可见 |
| **状态完全自主** | 会话、消息、工具结果都是公开可读写的类型 |
| **默认够用 + 不侵入可换** | 默认由作者选定；每个模块都能简单替换，包括 agent loop 本身 |
| **零依赖核心** | 核心只用 stdlib |

## 模块

| # | 模块 | 职责 |
|---|---|---|
| M1 | Agent Loop | 驱动「模型 → 工具 → 回填」迭代 |
| M2 | Tool | 把宿主能力暴露给模型 |
| M3 | Store | 会话状态的持久化契约 |
| M4 | 上下文管理 | 全部上下文处理策略 |
| M5 | Audit | agent 行为的不可变记录 |
| M6 | Hook | 同步拦截点 |
| M7 | Model | 与 LLM 交互的唯一边界 |

## 能力边界

**保证做到**：捕获所有组件回调里的 panic（含库自身默认组件），三通道上报，绝不静默。

**不保证**（进程级致命错误，任何进程内库都拦不住）：`os.Exit` · `runtime.Goexit` · 栈溢出 · OOM · 并发 map 写 · cgo 段错误。

本库只承诺 *recovers panics raised inside component callbacks*，**不承诺 "never crash"**。

## 文档

- [愿景与架构](docs/VISION.md) —— 定位、边界、模块划分、设计原则
- [构建计划](docs/BUILD_PLAN.md) —— 阶段划分、交付物、验收标准

## License

待定。
