# internal/api

HTTP server: REST cho dashboard, SSE stream realtime (run events, findings), webhook ingress (verify HMAC/token, dedupe vào `events`).

- Phụ thuộc: storage, config, orchestrator
- Milestone: M1 (webhook + health), M4 (REST + SSE đầy đủ)
- Xem: docs/UI.md#api
