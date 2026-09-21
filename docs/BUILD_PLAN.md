# TinyAgent 构建计划

> 配套 `docs/VISION.md` · 2026-09-21 · 状态：执行中

---

## 1. 第一版目标

验证 VISION 5.1 列出的五件事：

| # | 要验证的 | 验收方式 |
|---|---|---|
| 1 | 进程内嵌，无 CLI / 无 sidecar | 示例就是一个普通 `main` 函数 |
| 2 | **崩溃不带崩主程序** | 单元测试：工具里 panic，宿主进程存活且错误可见 |
| 3 | 几行代码接入 | 端到端示例 ≤ 10 行完成一次带工具的对话 |
| 4 | 状态完全自主 | 会话 / 消息 / 工具结果都是公开可读写类型 |
| 5 | 默认够用 + 不侵入可换 | 默认零配置可跑；替换任一模块不改其他代码 |

---

## 2. 技术决策

| 项 | 决策 | 理由 |
|---|---|---|
| module 路径 | `tinyagent`（暂定） | 本地开发够用；**发布前需改为完整仓库路径**，届时一次全局替换 |
| Go 版本 | `go 1.26` | 本地 `go1.26.3`；`iter.Seq2` 需 1.23+ |
| 依赖策略 | **第一版只用 stdlib** | 零依赖承诺天然成立。适配器的外部依赖留到后续，届时再拆多 module |
| 模块结构 | 第一版**单 module** | 多 module（`go.work`）的维护成本留到真正引入外部依赖时 |
| 流式 | `iter.Seq2[Event, error]` | Go 1.23+ range-over-func，比 callback 干净 |
| 并发 | Agent 无状态；Session 加锁 | 见 VISION §8 |
| 错误策略 | 统一 `Error` 类型；panic 三通道上报 | 见 VISION §4.3 |

---

## 3. 项目结构

```
tinyagent/
├── go.mod
├── README.md
├── LICENSE
├── docs/
│   ├── VISION.md              愿景（已定稿）
│   └── BUILD_PLAN.md          本文件
│
├── (根包 tinyagent)            ← 核心，只用 stdlib
│   ├── message.go             Role / Message / ToolCall / ToolResult
│   ├── event.go               Event / EventType
│   ├── usage.go               Usage
│   ├── errors.go              Error / ErrorKind / PanicPolicy
│   ├── recover.go             ★ 崩溃安全机制（横切）
│   ├── model.go               Model 接口 + Request / Response / Chunk
│   ├── tool.go                Tool 接口 + ToolInfo + 泛型注册
│   ├── store.go               Store 接口 + 内存实现
│   ├── context.go             Counter / Compressor 接口 + 默认策略
│   ├── hook.go                Hook 接口 + 链式组合
│   ├── audit.go               Audit 接口 + 内存实现
│   ├── session.go             Session
│   ├── runner.go              Runner 接口 + ReActRunner
│   └── agent.go               Agent 门面 + Config
│
├── model/
│   └── openai/                OpenAI 兼容适配器（stdlib HTTP + SSE）
│
└── examples/
    └── quickstart/main.go     端到端示例
```

**第一版全部只用 stdlib**，因此适配器可以放在同一 module 的子包里，不会引入外部依赖。

---

## 4. 阶段总览

| Phase | 目标 | 依赖 | 验收标准 |
|---|---|---|---|
| P0 | 项目骨架 | — | `go build ./...` 通过 |
| P1 | 核心类型 + 接口契约 | P0 | `go vet ./...` 通过，导出符号有文档注释 |
| P2 | ★ **崩溃安全机制** | P1 | 测试：工具 panic 后宿主存活，且错误可见 |
| P3 | Tool 模块 | P1, P2 | 泛型注册、多参数、执行、panic 转错误 |
| P4 | Model + OpenAI 适配器 | P1 | 真实调用成功；流式增量正确 |
| P5 | Store + Session | P1 | 多轮对话状态保持 |
| P6 | 上下文管理 | P1, P4 | 超长自动处理，工具配对完整 |
| P7 | Hook | P1, P2 | 链式组合；可否决调用 |
| P8 | Audit | P1 | 记录 + 查询；不阻塞主流程 |
| P9 | Runner（Agent Loop） | P3, P4, P6, P7 | mock 跑通多轮工具循环 |
| P10 | Agent 门面 | 全部 | 零配置可跑 |
| P11 | 端到端示例 + README | P10 | ≤10 行示例跑通真实 API |

**顺序理由**：P2（崩溃安全）刻意提前 —— 它是头号亮点，越早验证越好；晚做会导致所有模块都要返工加 recover。P9（Runner）放在最后，因为它依赖几乎所有模块。

---

## 5. 各阶段详情

### Phase 0 · 项目骨架

**目标**：可编译的空骨架 + 项目元信息。

**交付物**
- `README.md` —— 定位、快速开始占位、模块清单
- `LICENSE` —— 待定（Apache-2.0 或 MIT，需作者决定）
- 目录结构按 §3 建立

**验收**：`go build ./...` 与 `go vet ./...` 均通过。

**要点**：LICENSE 未定前不阻塞开发，但**发布前必须定**。

---

### Phase 1 · 核心类型 + 接口契约

**目标**：把 VISION §6 的模块边界落成 Go 类型与接口。

**交付物**
- `message.go`：`Role` / `Message` / `ToolCall` / `ToolResult`
- `event.go`：`Event` / `EventType`
- `usage.go`：`Usage`
- `errors.go`：`Error` / `ErrorKind` / `PanicPolicy`
- `model.go`：`Model` / `StreamingModel` 接口 + `Request` / `Response` / `Chunk`
- `tool.go`：`Tool` 接口 + `ToolInfo`
- `store.go`：`Store` 接口 + `SessionSnapshot`
- `context.go`：`Counter` / `Compressor` 接口
- `hook.go`：`Hook` 接口
- `audit.go`：`Audit` 接口
- `runner.go`：`Runner` 接口 + `RunnerOptions`

**验收**：`go vet ./...` 通过；每个导出符号有文档注释；接口只表达"做什么"不表达"怎么做"。

**要点**
- `Message` 的 JSON 表示从第一天起就是**稳定契约**
- `Store` 必须同时有 `Append` 和 `Replace`（压缩需要重写历史）
- `Model` 与 `StreamingModel` 分开，用类型断言探测能力
- `Counter` 的入参要能覆盖工具 schema（否则预算严重低估）
- **Hook 接口中不存在 panic 钩子**（见 VISION §4.3）

---

### Phase 2 · ★ 崩溃安全机制

**目标**：实现 VISION §4.1 / §4.3 的保证。**这是头号亮点，必须最早验证。**

**交付物**
- `recover.go`：统一的 recover 包装器
- 三通道上报：error 返回 + Event 推送 + `slog` 日志
- `PanicPolicy` 三种策略：`RecoverAsToolError`（默认）/ `FailRun` / `Propagate`

**验收**
- 单元测试：工具函数里 `panic("boom")` → 宿主进程存活，返回的 error 含组件名 / panic 值 / 堆栈
- 单元测试：Hook 里 panic → 该 Hook 被跳过，运行继续
- 单元测试：日志输出确实产生（可重定向到 buffer 断言）
- 单元测试：`PanicPolicy = Propagate` 时 panic 确实向外传播

**要点**
- **无豁免**：库自身默认组件的 panic 也必须走同一路径
- 报告**不可静默**：不可关闭，只可重定向
- 措辞纪律：测试名与注释都不能写 "never crash"

---

### Phase 3 · Tool 模块

**目标**：把宿主能力暴露给模型。

**交付物**
- `tool.go` 完善：`ToolInfo`（name / description / JSON Schema）
- 泛型注册：从 Go 结构体反射生成参数 schema
- 参数解析与校验
- 执行包装：超时 + panic 捕获 + 错误分类
- 结果规范化：业务软错误 vs 框架级失败

**验收**
- 注册一个**多参数**工具，schema 正确生成
- 调用不存在的工具 → 明确的框架错误
- 工具内 panic → 转为可见错误（走 P2 机制）
- 工具返回业务错误 → 作为观察喂回模型，循环不中断

**要点**
- **多参数是硬需求** —— 单参数是 langchaingo 已被证实的反模式
- 工具执行必须感知 `context`，否则取消无效并泄漏 goroutine
- 反射生成 schema 时，结构体 tag 的命名要一次定好（未来改动是破坏性的）

---

### Phase 4 · Model 接口 + OpenAI 兼容适配器

**目标**：打通与 LLM 的真实交互。

**交付物**
- `model.go` 完善：`Request` / `Response` / `Chunk` 完整定义
- `model/openai/`：OpenAI 兼容协议适配器（stdlib `net/http`）
  - 非流式生成
  - 流式生成（SSE）
  - 工具调用（含**分片聚合**）
  - 重试与退避（429 / 5xx）

**验收**
- 对真实 OpenAI 兼容端点完成一次非流式调用
- 流式调用的增量拼接结果 == 非流式结果
- 一次返回多个工具调用时，全部正确解析
- 429 时按退避重试

**要点**
- **流式工具调用是分片到达的**，`arguments` 需逐段拼接 —— 这是适配器的核心复杂度
- 适配器只做协议转换，**不含任何 agent 逻辑**
- 环境变量读取（`OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL`）方便零配置

---

### Phase 5 · Store + Session

**目标**：状态能存能取，多轮对话成立。

**交付物**
- `store.go`：内存 `Store` 实现（默认）
- `session.go`：`Session`（内存态、内部加锁、并发运行拒绝）
- `SessionSnapshot` 与 `Session` 的转换

**验收**
- 多轮对话后重新载入，历史完整
- `Replace` 能重写历史（供 P6 压缩使用）
- 同一 Session 并发 Run → 返回 `ErrSessionBusy`
- 不同 Session 并发 Run → 正常

**要点**
- `Load` 返回**纯数据快照**，不是内存对象（避免持久化格式绑死内存模型）
- 批量写必须**原子**：一次运行产生多条消息，不能留半截状态
- 内存实现也要保证并发安全

---

### Phase 6 · 上下文管理

**目标**：实现 VISION §6.4 M4 —— 全部上下文处理策略。

**交付物**
- `context.go` 完善：
  - `Counter`：默认 token 估算（覆盖消息 + 工具 schema + 系统提示 + 固定开销）
  - `Compressor`：默认策略（第一版至少实现**截断**与**压缩**两种）
  - 保护规则：系统提示、最近 N 轮、工具配对不可动
  - 处理事件：让接入方知道"发生了处理"

**验收**
- 超长对话触发处理，且**工具调用 ↔ 工具结果配对完整**（否则真实 API 返回 400）
- 处理后产生的消息能写回 Store
- 每种策略可独立替换
- 处理发生时有可见事件

**要点**
- **配对完整性是最容易踩的坑**，必须专门测试
- 预算计算必须覆盖工具 schema
- "卸载"策略第一版可只留接口，实现可延后（需先定落地位置，见 VISION §9）

---

### Phase 7 · Hook

**目标**：接入方同步介入 agent 行为。

**交付物**
- `hook.go` 完善：链式组合 + 四个拦截点（模型调用前后、工具调用前后）
- 允许修改请求 / 工具参数
- 允许否决（返回错误中断该次调用或整个运行）

**验收**
- 多个 Hook 按注册顺序执行
- Hook 返回错误 → 工具调用被否决，模型收到否决原因
- Hook 内 panic → 该 Hook 被跳过，运行继续（走 P2 机制）

**要点**
- **接口中不存在 panic 钩子**（VISION §4.3）
- Hook 在热路径同步执行，文档必须写明性能影响

---

### Phase 8 · Audit

**目标**：agent 行为可追溯。**第一版标准功能。**

**交付物**
- `audit.go`：`Audit` 接口 + 内存实现
- 记录点：会话生命周期、模型调用、工具调用、panic、上下文处理、Hook 否决
- 查询 / 导出能力

**验收**
- 一次完整运行后，能查到每次模型调用与工具调用（含参数、结果、耗时、状态）
- 审计写入失败 → 不阻塞主流程，但**必须上报**
- 默认开启

**要点**
- M5 **不被任何模块依赖**（旁路订阅）—— 这是"不阻塞主流程"的结构性保证
- 与 Hook 的区别：Hook 可改行为，Audit 只记录
- 存储位置 / 同步异步 / 缓冲上限属于实现细节，在实现时定

---

### Phase 9 · Runner（Agent Loop）

**目标**：把前面所有模块串成一个循环。

**交付物**
- `runner.go` 完善：`RunnerOptions` 注入面、默认 `ReActRunner`
- 迭代控制：上限、超时、终止条件
- 并行工具编排 + **结果保序**
- 事件产出（流式）
- 重试与退避

**验收**
- 用 mock Model 跑通"模型请求工具 → 执行 → 回填 → 再请求"多轮循环
- 一次返回多个工具调用 → 并行执行、结果保序
- 消费者提前退出 → context 取消、无 goroutine 泄漏
- 触达迭代上限 → 明确终止并产生事件

**要点**
- **事件产出只能在主 goroutine**，禁止在工具 goroutine 里直接产出
- 消费者提前退出时的清理最易遗漏，必须专门测试
- 替换 Runner 时，`RunnerOptions` 是真正的契约，不能漏发事件 / 漏调 Hook

---

### Phase 10 · Agent 门面

**目标**：接入方唯一直接接触的入口。零配置可跑。

**交付物**
- `agent.go`：`Config` / `New` / `Run` / `Tool` 注册 / Session 管理
- 默认值装配：默认内存 Store、默认 Counter、默认 Compressor、默认 Audit、默认 ReActRunner

**验收**
- **零配置**（只给模型信息）即可跑通一次带工具的对话
- 替换任一模块（如换 Store）不需要改动其他代码
- `Agent` 并发安全

**要点**
- **默认值必须由作者认真选定**（原则一）—— 默认质量决定"不侵入"承诺的真假
- 默认路径上用户能看到的模块越少越好

---

### Phase 11 · 端到端示例 + README

**目标**：证明"几行代码接入"。

**交付物**
- `examples/quickstart/main.go`
- `README.md` 完善：定位、快速开始、模块清单、边界声明

**验收**
- 示例 ≤10 行完成一次带工具的对话（不含工具函数本身）
- 示例能跑通**真实** API
- README 明确写出能力边界（含"拦不住什么"）

---

## 6. 主要风险

| # | 风险 | 缓解 |
|---|---|---|
| 1 | 反射生成 JSON Schema 的复杂度超预期 | 第一版可先支持基础类型；复杂类型允许手写 schema 兜底 |
| 2 | 流式 + 并行工具 + 事件产出的组合陷阱 | P9 专门测试取消与清理；严格遵守"主 goroutine 产出" |
| 3 | 压缩破坏工具配对 → 真实 API 400 | P6 专项测试配对完整性 |
| 4 | panic 覆盖有遗漏（库内部 goroutine） | P2 建立统一包装器，所有 goroutine 必须走它；用测试覆盖 |
| 5 | 默认值选得不好，导致"不侵入"退化为"必须侵入" | P10 用端到端示例倒逼默认值质量 |

---

## 7. 第一版之后的 TODO

来自 VISION §5.2：

- 沙箱隔离
- 硬资源限制（CPU / 内存 / 调用次数）
- agent 编排（**非 DAG**）
- `store/sqlite`、`store/mysql` 适配器（届时拆多 module）
- MCP 工具来源（复用现成 Go SDK）
- Anthropic 等更多 provider 适配
- 上下文"卸载"策略的具体实现
- LICENSE 最终选定
