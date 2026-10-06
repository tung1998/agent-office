# internal/worker

Thực thi worker request: dựng strict MCP config theo allowlist, verify tool surface, gọi runtime model rẻ, validate output `{request_id, query, data, source, timestamp, summary}`, lưu raw data vào `worker_outputs`.

- Phụ thuộc: runtime, mcp, storage
- Milestone: M0 (1 worker), M1+
