# internal/runtime

`RuntimeAdapter` interface, `Router` (fallback chain, budget check trước khi gọi), `ResultEnvelope` chuẩn hóa. Adapter con: `claudecode/`, `api/` (M0), `codex/`, `gemini/` (M5).

- Phụ thuộc: `internal/budget`, `internal/audit`
- Milestone: M0 (interface + claude-code + api), M5 (codex, gemini)
- Xem: docs/INTERFACES.md#runtimeadapter
