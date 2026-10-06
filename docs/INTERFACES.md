# Interfaces

Đặc tả bằng chữ ký Go để thống nhất hợp đồng. **Đây là thiết kế, chưa phải code.** Tên package theo cây thư mục trong `internal/`.

## RuntimeAdapter

```go
package runtime

type Adapter interface {
    Name() string                          // "claude-code", "api", "codex", "gemini"
    Capabilities() Capabilities
    Probe(ctx context.Context) ProbeResult // cài chưa, version, đăng nhập/API key
    Run(ctx context.Context, req Request, onEvent func(Event)) (ResultEnvelope, error)
}

type Capabilities struct {
    ID              string
    BuiltinTools    string   // "granular" | "sandbox" | "none"
    PermissionModes []string // rỗng = không áp dụng
    Efforts         []string
    MCP             string   // "config-file" | "inline" | "native-client" | "none"
    SessionResume   bool
    StructuredOut   bool     // hỗ trợ ép JSON schema
    CostReported    bool     // vendor trả cost thật
    ToolSurfaceCheck bool    // có event init liệt kê tool để verify
}

type Request struct {
    RunID          string
    AgentID        string
    Model          string
    Effort         string
    SystemPrompt   string        // role template + knowledge + memory
    Prompt         string        // context của run, qua stdin với CLI
    OutputSchema   json.RawMessage // JSON Schema output mong đợi
    MCPServers     []MCPServer   // đã lọc theo allowlist
    ToolsAllow     []string
    PermissionMode string
    ResumeSession  string
    Timeout        time.Duration
    WorkDir        string
    Env            map[string]string // secret truyền qua env, không qua argv
}

type Event struct {
    Type string          // text | tool_call | tool_result | system | error
    Data json.RawMessage
    At   time.Time
}

// Chuẩn hóa output mọi runtime về một envelope.
type ResultEnvelope struct {
    IsError          bool
    Text             string
    JSON             json.RawMessage // khi StructuredOut
    Model            string
    SessionID        string
    NumTurns         int
    DurationMS       int64
    Usage            Usage
    CostUSD          float64
    CostEstimated    bool
    ToolsSeen        []string        // từ event init, phục vụ verify
}

type Usage struct {
    InputTokens      int // fresh, không gồm cache
    CacheReadTokens  int
    CacheWriteTokens int
    OutputTokens     int
}

type ProbeResult struct {
    Available bool
    Version   string
    Detail    string
}
```

**Phân loại lỗi.** Adapter trả error bọc `*runtime.Error{Class, Retryable, Fallbackable}`.

| Class | Fallback | Retry |
|---|---|---|
| `unavailable` (binary không có, chưa login, thiếu key) | có | không |
| `quota`, `rate_limit` | có | sau backoff |
| `timeout` | có | 1 lần |
| `output_contract` | không | 1 lần kèm lỗi validate |
| `tool_surface` | không | không |
| `internal` | không | theo `max_attempts` |

### Router

```go
type Router interface {
    // Chọn adapter theo agent.runtime/model, thử fallback theo thứ tự,
    // kiểm budget trước mỗi lần thử, ghi fallback_index.
    Run(ctx context.Context, agent config.Agent, req Request, onEvent func(Event)) (ResultEnvelope, Attempt, error)
}

type Attempt struct {
    Runtime       string
    Model         string
    FallbackIndex int
}
```

### Ghi chú theo adapter

| Adapter | Gọi | Output | Ghi chú |
|---|---|---|---|
| `claude-code` | `claude -p --output-format stream-json --verbose --model X --append-system-prompt-file F --strict-mcp-config --mcp-config F --allowedTools …` | stream-json, cộng dồn nhiều `result` | Prompt qua stdin. Verify tool từ `system/init`. Resume `--resume ID --fork-session` |
| `api` | Anthropic Messages hoặc OpenAI-compatible `/chat/completions` | tự dựng envelope, `cost_estimated=true` với OpenAI-compatible | Tool loop tự viết, MCP client riêng, giới hạn turn |
| `codex` (M5) | `codex exec --json -m X -s read-only -c mcp_servers.*` | JSONL | Sandbox là giới hạn tool |
| `gemini` (M5) | `gemini -p --output-format json -m X` | JSON | Kiểm tra MCP config theo version khi làm |

## StorageAdapter

```go
package storage

type Store interface {
    Tx(ctx context.Context, fn func(Tx) error) error
    Migrate(ctx context.Context) error
    Close() error

    Agents() AgentRepo
    Memory() MemoryRepo
    Schedules() ScheduleRepo
    Events() EventRepo
    Runs() RunRepo
    WorkerOutputs() WorkerOutputRepo
    Blackboard() BlackboardRepo
    Incidents() IncidentRepo
    Approvals() ApprovalRepo
    Costs() CostRepo
    Audit() AuditRepo
    Blobs() BlobRepo
}

type RunRepo interface {
    Enqueue(ctx context.Context, r NewRun) (Run, error)
    Claim(ctx context.Context, workerID string, lease time.Duration) (*Run, error) // nil khi rỗng
    Heartbeat(ctx context.Context, runID string, lease time.Duration) error
    Finish(ctx context.Context, runID string, res RunResult) error
    RecoverExpired(ctx context.Context, now time.Time) (int, error)
    AppendEvent(ctx context.Context, runID string, e runtime.Event) error
    Get(ctx context.Context, id string) (Run, error)
    List(ctx context.Context, f RunFilter) ([]Run, error)
}

type ScheduleRepo interface {
    Due(ctx context.Context, now time.Time, limit int) ([]Schedule, error)
    SetNext(ctx context.Context, agentID string, next time.Time, mode string, cleanStreak int) error
    MarkChecked(ctx context.Context, agentID string, at time.Time) error
}

type MemoryRepo interface {
    Current(ctx context.Context, agentID, layer string) (MemoryDoc, error)
    Write(ctx context.Context, d NewMemoryDoc) (MemoryDoc, error) // version mới
    AppendEntry(ctx context.Context, e MemoryEntry) error
    PendingEntries(ctx context.Context, agentID string) ([]MemoryEntry, error)
    History(ctx context.Context, agentID, layer string, limit int) ([]MemoryDoc, error)
}

type BlackboardRepo interface {
    CreateThread(ctx context.Context, t NewThread) (Thread, error)
    // Post enforce evidence (ADR-007) trong cùng transaction.
    Post(ctx context.Context, f NewFinding, evidence []EvidenceRef) (Finding, error)
    // View lọc theo góc nhìn: ở pha independent chỉ trả finding của viewer + observation chung.
    View(ctx context.Context, threadID string, viewer string, phase string) ([]Finding, error)
    SetPhase(ctx context.Context, threadID, phase string, round int) error
}

type CostRepo interface {
    Record(ctx context.Context, e CostEntry) error
    Spent(ctx context.Context, scope, scopeID string, since time.Time) (float64, error)
    Daily(ctx context.Context, from, to time.Time) ([]CostDaily, error)
}
```

Các repo còn lại theo cùng kiểu CRUD tối thiểu. Mọi driver phải pass **một bộ contract test chung** (`internal/storage/storagetest`).

## NotifyAdapter

```go
package notify

type Adapter interface {
    Name() string // "discord", "telegram", "slack"
    Send(ctx context.Context, msg Message) (Delivery, error)
}

// Tùy chọn: kênh hỗ trợ nhận tin để làm on-demand ask.
type Inbound interface {
    Listen(ctx context.Context, handle func(InboundMessage)) error
}

type Message struct {
    Severity  string // info | low | medium | high | critical
    Title     string
    Body      string // markdown, adapter tự chia nhỏ theo giới hạn kênh
    Links     []Link // tới incident, evidence trên dashboard
    Actions   []Action // nút Duyệt/Từ chối nếu kênh hỗ trợ
    ThreadKey string // gom tin cùng incident
}
```

Router notify đọc `notify.routes` trong config (severity → kênh). Agent không gọi notify trực tiếp (ADR-008).

## TriggerSource

```go
package trigger

type Source interface {
    Name() string                  // "sentry", "graylog", "generic"
    Verify(r *http.Request, body []byte, secret string) error
    Parse(body []byte) (Event, error) // dedupe_key, severity, summary
}
```

## RoleTemplate

```go
package roles

type Template struct {
    Name         string          // "tech-lead"
    Level        string          // director | manager | worker
    Prompt       string          // nội dung templates/roles/<name>.md
    OutputSchema json.RawMessage // schema output của vai trò
    Defaults     RoleDefaults    // model tier, heartbeat, memory limits
}
```

Tra cứu theo thứ tự: `.office/roles/<name>.md` của project, rồi plugin, rồi `templates/roles/` built-in.

## Hợp đồng output

### Worker output

Worker trả đúng schema sau. `data` là dữ liệu đã cắt gọn cho LLM đọc. Raw đầy đủ được lưu riêng ở `raw_ref` do `internal/worker` ghi, không phải do model.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "agent-office/worker-output",
  "type": "object",
  "required": ["request_id", "query", "data", "source", "timestamp", "summary"],
  "additionalProperties": false,
  "properties": {
    "request_id": { "type": "string" },
    "query": { "type": "string" },
    "data": {},
    "source": {
      "type": "object",
      "required": ["server", "tool"],
      "properties": {
        "server": { "type": "string" },
        "tool": { "type": "string" },
        "args": { "type": "object" }
      }
    },
    "timestamp": { "type": "string", "format": "date-time" },
    "summary": { "type": "string", "maxLength": 500 },
    "truncated": { "type": "boolean" },
    "errors": { "type": "array", "items": { "type": "string" } }
  }
}
```

### Manager output (heartbeat analyze / debate)

```json
{
  "$id": "agent-office/manager-output",
  "type": "object",
  "required": ["working_memory", "findings", "next_check_in_minutes"],
  "properties": {
    "working_memory": { "type": "object", "description": "Toàn bộ layer working mới" },
    "long_term_entries": {
      "type": "array",
      "items": { "type": "object", "required": ["kind", "text"], "properties": {
        "kind": { "enum": ["pattern", "lesson", "noise", "fact"] },
        "text": { "type": "string" }, "refs": { "type": "array", "items": { "type": "string" } } } }
    },
    "worker_requests": {
      "type": "array",
      "items": { "type": "object", "required": ["worker_id", "query"], "properties": {
        "worker_id": { "type": "string" }, "query": { "type": "string" },
        "since": { "type": "string", "format": "date-time" } } }
    },
    "findings": {
      "type": "array",
      "items": { "type": "object", "required": ["kind", "body"], "properties": {
        "kind": { "enum": ["observation", "hypothesis", "question", "rebuttal"] },
        "body": { "type": "string" },
        "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
        "evidence_ids": { "type": "array", "items": { "type": "string" } },
        "reply_to": { "type": "string" },
        "addressed_to": { "type": "string" },
        "severity": { "enum": ["info", "low", "medium", "high", "critical"] } } }
    },
    "escalate": {
      "type": "object",
      "properties": { "severity": { "type": "string" }, "reason": { "type": "string" },
        "finding_refs": { "type": "array", "items": { "type": "string" } } }
    },
    "mode": { "enum": ["normal", "suspicious", "incident"] },
    "next_check_in_minutes": { "type": "integer", "minimum": 1 }
  }
}
```

`worker_requests` khác rỗng thì orchestrator chạy worker rồi enqueue analyze run. `findings` chỉ được ghi sau khi evidence tồn tại.

### Director output (synthesis)

```json
{
  "$id": "agent-office/director-conclusion",
  "type": "object",
  "required": ["conclusion", "confidence", "evidence_finding_ids", "options", "risks"],
  "properties": {
    "conclusion": { "type": "string" },
    "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
    "evidence_finding_ids": { "type": "array", "minItems": 1, "items": { "type": "string" } },
    "options": {
      "type": "array",
      "items": { "type": "object", "required": ["title", "side_effect"], "properties": {
        "title": { "type": "string" }, "description": { "type": "string" },
        "side_effect": { "type": "boolean" },
        "action": { "type": "object", "description": "worker_id + tool + args khi side_effect" },
        "risk": { "type": "string" } } }
    },
    "risks": { "type": "array", "items": { "type": "string" } },
    "open_questions": { "type": "array", "items": { "type": "string" } },
    "notify": { "type": "object", "properties": { "severity": { "type": "string" }, "summary": { "type": "string" } } }
  }
}
```

Option có `side_effect=true` tạo `approval_request`. Director output cho `ask` và báo cáo sáng dùng schema tương tự, bỏ `options` bắt buộc.

## API token cá nhân

Token `ofc_…` cho Claude Code CLI của chính người dùng gọi MCP của office (ADR-047, ADR-094). Đều cần đăng nhập; mỗi người chỉ thấy token của mình.

| Lệnh | Vào | Ra |
|---|---|---|
| `GET /api/me/tokens` | | `{"tokens":[{"id","name","created_at","last_used_at","expires_at","expired"}]}` |
| `POST /api/me/tokens` | `{"name"?: string, "days"?: 30\|90\|365\|0}` | `201 {"id","name","token","expires_at"}`; `token` chỉ hiện lần này |
| `DELETE /api/me/tokens/{id}` | | `204` |

- `days` mặc định 90; `0` là không hạn (`expires_at: null`); giá trị khác trả `400`.
- `expires_at` là thời điểm hết hạn (RFC 3339, UTC) hoặc `null`. `expired: true` khi đã quá hạn; token hết hạn hoặc đã thu hồi bị từ chối.
- Quyền theo role của người tạo, đọc lại ở mỗi lần gọi: ai cũng được đề xuất, chỉ admin gọi thẳng tool MCP có ghi qua cổng.

## Plugin

```go
// Mỗi loại có registry riêng, đăng ký trong init() của plugin.
runtime.Register("gemini", func(cfg config.Runtime) (runtime.Adapter, error) { ... })
notify.Register("slack", func(cfg config.NotifyChannel) (notify.Adapter, error) { ... })
storage.Register("postgres", func(cfg config.Storage) (storage.Store, error) { ... })
trigger.Register("sentry", func() trigger.Source { ... })
roles.Register(roles.Template{Name: "sre", ...})
```

Binary mặc định import plugin built-in. Binary tùy biến tạo `cmd/office-custom/main.go` với import `_ "github.com/org/office-plugin-x"`. Config tham chiếu plugin theo tên. Tên không có trong registry thì `doctor` báo lỗi.
