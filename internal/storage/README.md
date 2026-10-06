# internal/storage

`StorageAdapter` + repository cho từng bảng. Hai driver: `sqlite/` (modernc.org/sqlite, local) và `postgres/` (pgx/v5, server/Supabase). Queue claim: SKIP LOCKED (PG) / single-writer UPDATE…RETURNING (SQLite).

- Phụ thuộc: `migrations/`
- Milestone: M0
- Xem: docs/DATA_MODEL.md
