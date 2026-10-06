# templates/roles

Prompt khung cho từng vai trò. Config trỏ tới template qua `role_template`. Project có thể override bằng `.office/roles/<name>.md`.

| File | Cấp | Output schema |
|---|---|---|
| `director.md` | director | `director-conclusion` |
| `tech-lead.md`, `data-analyst.md`, `risk-security.md` | manager | `manager-output` |
| `worker.md` | worker | `worker-output` |
| `_shared-rules.md` | tất cả | Khối quy tắc chung, chèn vào cuối mọi prompt |

Cú pháp placeholder kiểu Mustache: `{{project.name}}`, `{{#managers}}…{{/managers}}`, `{{> _shared-rules}}`. `{{memory}}` và `{{context}}` do orchestrator dựng (xem docs/ARCHITECTURE.md mục 3). Frontmatter khai báo `model_tier` để `init` chọn model mặc định.

Đây là khung, sẽ tinh chỉnh bằng dữ liệu thật ở M1–M2.
