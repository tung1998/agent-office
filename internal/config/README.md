# internal/config

Đọc, validate (JSON Schema), ghi `office.config.json`. Resolve secret qua env (`*_env`). Tính diff giữa config hiện tại và đề xuất mới (dùng cho `init`, `tune`, UI).

- Phụ thuộc: `schema/office.config.schema.json`
- Milestone: M0
