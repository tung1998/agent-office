# cmd/office

Entry point của binary `office` (CLI + server). Chỉ parse flag, load config, wire các package trong `internal/`. Không chứa business logic.

- Phụ thuộc: `internal/*`
- Milestone: M0
