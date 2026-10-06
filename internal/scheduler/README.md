# internal/scheduler

Ticker poll bảng `schedules` (next_check_at, cron) và `events` (webhook inbox), enqueue `runs`. Worker pool claim run và giao cho orchestrator. An toàn khi restart (trạng thái nằm trong DB).

- Phụ thuộc: `internal/storage`, `internal/orchestrator`
- Milestone: M1
