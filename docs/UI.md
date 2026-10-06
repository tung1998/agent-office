# Dashboard

Nuxt 4 + @nuxt/ui, app riêng trong `dashboard/` (ADR-010). Gọi REST + SSE của `office` server. Ngôn ngữ vi/en.

## Màn hình

Dự án thật (`dashboard/app/pages/`) là projects-based, không phải sơ đồ tổ chức/blackboard như bản thiết kế sơ khai; bảng dưới đây khớp code hiện tại.

| Route | Màn hình | Nội dung chính | Hành động |
|---|---|---|---|
| `/` | Tổng quan | 3 tab qua `?tab=`: **Việc** (mặc định — cần xử lý mọi project trừ Trợ lý office, việc gần đây, badge đếm trên sidebar), **Thống kê** (chi phí/lượt chạy theo ngày · project · model, 7/30/90 ngày — ADR-060), **Máy** (admin: trạng thái máy, kết nối AI, bước thiết lập ban đầu) | |
| `/projects` | Danh sách project | Project đang quản lý (ADR-017: có thư mục, clone git, hoặc không thư mục — toàn máy) | Thêm project (chọn mẫu mô hình tổ chức) |
| `/projects/:id?tab=…` | Project | Thanh tiêu đề gọn (menu `⋯` sửa tên/mô tả, thiết lập AI, đổi mô hình), 1 dòng thông tin (đường dẫn · git · mô tả rút gọn). Tab: **Chat**, **Tự động hoá** (lịch/webhook/bot), **Vận hành** (Tiến trình · Container · Giám sát), **Mô hình** (sơ đồ agent), **Quyền** (lệnh, file cấm sửa); chỉ admin thấy thêm: **Files**, **Skill**, **MCP**, **Burn**, **Nhật ký sửa**; và **Thông tin** (budget, git, mô tả) | tuỳ tab |
| `/projects/:id/setup` | Thiết lập AI | Quét project, đọc agent có sẵn, đề xuất mô hình tổ chức + tinh chỉnh agent (ADR-018) | Tạo / Chỉnh / Hủy |
| `/projects/:id/agents/:agentId` | Agent | Số liệu (lượt chạy, chi phí, tỉ lệ lỗi theo ngày/model), việc + chat gần đây, lịch sử sửa cấu hình (khôi phục được) | Sửa cấu hình, từng thẻ tự lưu |
| `/projects/:id/automations/new`, `/automations/:aid`, `/automations/:aid/edit` | Tự động hoá | Tạo/sửa 1 automation (lịch, webhook, điều kiện, script hoặc chat); xem 1 cái: trạng thái, bật/tắt, Việc đã chạy | Chạy thử, sửa, xoá, copy URL webhook |
| `/projects/:id/bots/new`, `/bots/:bid`, `/bots/:bid/edit` | Bot (Discord/Telegram) | Kết nối bot (ADR-049), trạng thái, danh sách lệnh (mỗi lệnh là 1 automation riêng), Việc đã chạy | Kết nối/ngắt, thêm/sửa lệnh |
| `/projects/:id/skills/edit?name=&scope=` | Soạn skill | Soạn skill của project hoặc riêng máy (`?scope=user`) bằng chat có theo dõi (ADR-062) | Lưu |
| `/library` (admin) | Thư viện | Skills / MCP servers: **Đã cài** (mọi nơi trên máy, nhóm theo nơi cài), **Thư viện**, MCP **Phổ biến**, **Tìm MCP** | Xem, cài vào nơi khác, lưu vào thư viện, gỡ, tạo và sửa |
| `/templates`, `/templates/new`, `/templates/:id` | Mẫu mô hình tổ chức | Danh sách mẫu (3 mẫu có sẵn + mẫu tự lưu từ project), tạo mới, sửa 1 mẫu | Tạo / Sửa / Xoá / Export |
| `/jobs` | Job | Mọi lượt chạy (chat, Việc, automation) của mọi project: trạng thái, lọc theo project, lỗi 24h, chi phí 24h | |
| `/providers` | Kết nối AI | Danh sách kết nối (Anthropic, OpenAI, API tương thích, CLI…), gửi thử, thống kê theo kết nối | Thêm / Sửa / Xoá / Đặt mặc định |
| `/watch` | Theo dõi | Nhiều khung chat của các project cạnh nhau, chia dọc/ngang, lưu trong trình duyệt (không đồng bộ máy khác) | Thêm/xoá khung |
| `/assistant` | Trợ lý office | Chat với trợ lý office (project ảo, không gắn thư mục, dùng để quản lý chính office) | |
| `/account` | Tài khoản | Đổi tên/mật khẩu, ngôn ngữ, giao diện sáng/tối, cài CLI | |
| `/admin/users` (admin) | Người dùng | Danh sách tài khoản, tạo, đặt lại mật khẩu, bật/tắt (tắt thu hồi session) | |
| `/admin/audit` (admin) | Nhật ký thao tác | Lịch sử thay đổi cấu hình/quyền toàn máy | |
| `/admin/transfer` (admin) | Chuyển máy | Export/import toàn bộ cấu hình (backup sang máy khác) | |
| `/admin/update` (admin) | Cập nhật office | Build lại từ mã nguồn, xem log trực tiếp, tự quay về bản cũ nếu lỗi | Cập nhật, kiểm tra có bản mới |
| `/login` | Đăng nhập | | |

`/costs` và `/incidents` đã bỏ, gộp vào Tổng quan (`/`): biểu đồ chi phí + danh sách cần xử lý (ADR-060, ADR-051); hai đường dẫn cũ chuyển hướng về `/`. Không có trang `/setup` (chỉ `/projects/:id/setup`), `/agents/:id` (thay bằng `/projects/:id/agents/:agentId`), `/blackboard`, hay `/ask` — đó là thiết kế sơ khai ban đầu, chưa từng lên app thật.

Sidebar 4 nhóm (xem `dashboard/app/layouts/default.vue`): **Project** (5 project mở nhiều nhất gần đây — đếm trong localStorage, mỗi project mở rộng ra các tab con; "Tất cả project" mở `/projects`); **Làm việc** (Trợ lý office, Theo dõi, Tổng quan có badge đếm, Job); **Cài đặt** (Kết nối AI, Mẫu mô hình tổ chức, Thư viện — chỉ admin); **Admin** (Người dùng, Nhật ký, Chuyển máy, Cập nhật).

## Luồng người dùng

**Lần đầu (thêm project).**
1. Mở `/projects`, thêm project (thư mục có sẵn, clone git, hoặc không thư mục — toàn máy, ADR-017).
2. Vào `/projects/:id/setup`. Chưa có kết nối AI sẵn sàng thì nối trước ở `/providers`.
3. `POST /api/projects/:id/setup/scan` quét project, rồi `POST /api/projects/:id/setup/propose` đề xuất mô hình tổ chức + tinh chỉnh agent (gọi LLM, kèm `reason` và ước tính chi phí) — ADR-018.
4. Đổi mẫu/lựa chọn thì gọi lại `POST /api/projects/:id/setup/build` để xem preview mới. Bấm Tạo gọi `POST /api/projects/:id/setup/apply`.
5. Chuyển vào `/projects/:id`, thấy Chat/Việc/Tự động hoá của project.

**Hằng ngày.**
1. Nhận Discord "cần xử lý" có link về `/` (Tổng quan — ADR-051).
2. Mở mục ứng với sự cố, xử lý tại chỗ xảy ra (project, tự động hoá, bot…).

**Tinh chỉnh agent.**
1. Từ project mở tab **Mô hình** (`/projects/:id?tab=model`), chọn 1 agent để vào `/projects/:id/agents/:agentId`.
2. Đổi model hoặc sửa prompt override; mỗi thẻ cấu hình tự lưu khi sửa (`PATCH /api/agents/:id`).
3. Xem số liệu (lượt chạy, chi phí, tỉ lệ lỗi) và lịch sử sửa; khôi phục lại bản cũ nếu cần (`POST /api/agents/:id/restore`).

## API

Base `/api`. Auth bằng cookie phiên `office_session` sau khi đăng nhập tài khoản (ADR-014). Role `admin` quản lý tài khoản và audit, `member` chỉ xem. Passkey để sau.

### Kết nối AI, mô hình, repo (đã làm)
| Method | Path | Quyền | Mô tả |
|---|---|---|---|
| GET | `/api/system` | đăng nhập | Chế độ cài đặt, thư mục dữ liệu |
| GET | `/api/provider-kinds` | đăng nhập | Các loại kết nối và gợi ý |
| GET | `/api/providers` | đăng nhập | Danh sách, không có key |
| POST/PATCH/DELETE | `/api/providers[/:id]` | admin | `api_key` bỏ qua = giữ, `""` = xóa |
| POST | `/api/providers/:id/default` | admin | Đặt mặc định |
| POST | `/api/providers/:id/test` | admin | `{prompt?, model?}` |
| GET | `/api/templates` | đăng nhập | Thư viện mẫu |
| POST | `/api/templates` | admin | `{source_id, key, name}` nhân bản, hoặc `{template}` import |
| POST | `/api/templates/:key/reset` | admin | Khôi phục mẫu có sẵn |
| GET/PATCH/DELETE | `/api/org-models/:id` | đăng nhập/admin | Mô hình kèm agent; sửa tên, loại, governance |
| GET | `/api/org-models/:id/export` | đăng nhập | JSON Template |
| POST | `/api/org-models/:id/agents` | admin | Thêm agent |
| PATCH/DELETE | `/api/agents/:id` | admin | Sửa/xóa agent; lỗi cấu trúc trả `problems[]` |
| GET/POST | `/api/projects` | đăng nhập/admin | `{path?, name?, template_id?}`; bỏ trống path = helper toàn máy (bắt buộc name) |
| GET/PATCH/DELETE | `/api/projects/:id` | đăng nhập/admin | Xóa chỉ bỏ quản lý, không đụng file |
| POST | `/api/projects/:id/model` | admin | `{template_id, replace}` |
| GET | `/api/fs/dirs?path=&hidden=1` | admin | Thư mục con cho cây chọn folder, kèm shortcut |
| GET | `/api/org-models/:id/revisions` | đăng nhập | Lịch sử chỉnh sửa |
| GET | `/api/revisions/:id` | đăng nhập | Snapshot |
| POST | `/api/revisions/:id/restore` | admin | Khôi phục (tạo snapshot hiện tại trước) |
| GET | `/api/projects/:id/chat/agents` | đăng nhập | Agent trò chuyện được |
| GET/POST | `/api/projects/:id/conversations` | đăng nhập | Danh sách / tạo cuộc trò chuyện `{agent_id?}` |
| GET/DELETE | `/api/conversations/:id` | đăng nhập | Tin nhắn kèm đề xuất diff |
| POST | `/api/conversations/:id/messages` | đăng nhập | `{text}` → `turn_id`; 429 hết ngân sách, 409 đang bận |
| GET | `/api/chat/turns/:id/stream` | đăng nhập | SSE: text, tool, status, patch, done, error |
| POST | `/api/chat/turns/:id/cancel` | đăng nhập | Dừng |
| GET/POST | `/api/projects/:id/tasks` | đăng nhập | Danh sách / giao việc `{goal, budget_usd}` |
| GET/DELETE | `/api/tasks/:id` | đăng nhập/admin | Việc kèm bước và diff |
| GET | `/api/tasks/:id/stream` | đăng nhập | SSE: step, text, tool, step_done, patch, status, done |
| POST | `/api/tasks/:id/cancel` | đăng nhập | Dừng |
| POST | `/api/patches/:id/approve` \| `/reject` | admin | Áp diff bằng git apply / từ chối |
| GET | `/api/cli-tools[/:id]` | admin | Claude Code / Codex: đã cài, version, đăng nhập, cách cài |
| POST | `/api/cli-tools/:id/install` | admin | `{method}`: native \| brew \| npm |
| POST | `/api/cli-tools/:id/login` | admin | Bắt đầu đăng nhập |
| GET | `/api/cli-jobs/:id` | admin | Output, link, mã thiết bị, trạng thái |
| POST | `/api/cli-jobs/:id/input` \| `/cancel` | admin | Gửi mã / hủy |
| GET | `/api/usage/summary?days=` | đăng nhập | Chi phí hôm nay, theo ngày, theo project, theo model |
| GET | `/api/usage/runs?project=&days=&limit=` | đăng nhập | Lượt gọi gần đây |
| PUT | `/api/usage/settings` | admin | Trần ngày, trần theo project, giá model |
| GET | `/api/transfer/export` | admin | Tải bundle config |
| POST | `/api/transfer/import` | admin | `{bundle, dry_run}` → danh sách thay đổi |
| POST | `/api/transfer/backup` | admin | Tạo backup trên máy chạy office |
| POST | `/api/projects/:id/setup/scan` | admin | Quét tĩnh, trả summary |
| POST | `/api/projects/:id/setup/propose` | admin | `{goal?}` → đề xuất AI; `412 no_provider` khi chưa có kết nối |
| POST | `/api/projects/:id/setup/build` | admin | `{template_key, changes}` → template + problems (xem trước) |
| POST | `/api/projects/:id/setup/apply` | admin | `{template_key, changes, description}` → cài mô hình cho project |

### Kết nối AI: preset và thống kê (đã làm, ADR-028)

| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/provider-kinds` | thêm `presets`: nhà cung cấp bên thứ 3 (id, nhóm, base_url, key_url, key_env, need_key) |
| GET | `/api/providers/stats?days=7` | `today` + mỗi kết nối: calls, errors, token, cost, avg_ms, last_used_at, top_model, days[] |

Tạo/sửa kết nối nhận thêm `preset`.

### Tự động hóa (đã làm, admin)

| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/automation/scan` | `{items, projects}`: skill/agent/MCP đã cài và project Claude Code đã mở |
| POST | `/api/automation/content` | ref → nội dung (MCP đã che bí mật) |
| POST | `/api/automation/install` | `{kind, name, target{scope,project_path}, library\|from\|template, values, overwrite, accept}`; 409 `exists`/`consent`, 422 `unsafe` kèm `findings` |
| POST | `/api/automation/remove` | ref → gỡ (skill/agent vào thùng rác) |
| POST | `/api/automation/save-to-library` | `{ref, name}` |
| POST | `/api/automation/check` | `{files}` → findings |
| GET/PUT/DELETE | `/api/automation/library/:kind[/:name]` | thư viện |
| GET | `/api/automation/mcp/catalog` | MCP phổ biến |
| GET | `/api/automation/mcp/registry?q=` | tìm trong MCP Registry |
| GET | `/api/automation/mcp/status?path=` | kết quả `claude mcp list` mới nhất của thư mục (`""` = toàn máy) (ADR-088) |
| POST | `/api/automation/mcp/status/check` | `{path}` → bắt đầu kiểm tra; xong thì đẩy event `mcp.status` |
| GET | `/api/jobs` | Việc của mọi project (mọi user) |

### Skill và đính kèm trong Chat/Việc (đã làm, ADR-025)

| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/projects/:id/skills` | skill gọi được bằng `/tên` (project, máy, plugin) |
| POST | `/api/projects/:id/attachments` | `{name, data(base64)}` → `{id, name, kind, mime, size}` |
| GET | `/api/attachments/:id` | xem file (ảnh/PDF giữ loại, còn lại text/plain, sandbox) |

Gửi tin nhắn `{text, attachments:[id]}`; tạo Việc `{goal, budget_usd, attachments:[id]}`.

### Vận hành (đã làm, ADR-026)

| Method | Path | Quyền | Mô tả |
|---|---|---|---|
| GET | `/api/projects/:id/ops/detect` | admin | lệnh tìm thấy trong project + file compose |
| GET/POST | `/api/projects/:id/processes` | auth/admin | danh sách kèm trạng thái / thêm lệnh |
| PATCH/DELETE | `/api/processes/:id` | admin | sửa / xóa (dừng nếu đang chạy) |
| POST | `/api/processes/:id/start\|stop\|restart` | admin | điều khiển |
| GET | `/api/processes/:id/stream` | auth | SSE: `lines` (phát lại từ `Last-Event-ID`), `state` |
| POST | `/api/processes/:id/log-attachment` | auth | 300 dòng log cuối thành file đính kèm |
| GET | `/api/projects/:id/compose?file=` | auth | docker, file compose, service + container, cổng, stats |
| POST | `/api/projects/:id/compose/action` | admin | `{file, action: up\|stop\|restart\|down\|pull\|build, service?}` chạy nền |
| GET | `/api/projects/:id/compose/action/stream` | auth | SSE output thao tác gần nhất |
| GET | `/api/projects/:id/compose/logs?file=&service=` | auth | SSE log container (`--follow --tail 300`) |
| POST | `/api/projects/:id/compose/log-attachment` | auth | `{file, service}` → log container thành file đính kèm |

### Giám sát (đã làm, ADR-027)

| Method | Path | Quyền | Mô tả |
|---|---|---|---|
| GET | `/api/monitors?project=` | auth | giám sát + uptime 24h, độ trễ TB, 30 lần kiểm tra gần nhất; `summary` |
| POST | `/api/projects/:id/monitors` | admin | `{name, type, target, config, interval_s, ai_enabled, ai_budget_usd}` |
| PATCH/DELETE | `/api/monitors/:id` | admin | sửa (tạm dừng = `enabled:false`) / xóa |
| POST | `/api/monitors/:id/check` | admin | kiểm tra ngay |
| GET | `/api/monitor-events?project=&limit=` | auth | sự kiện Up/Down kèm phân tích AI |
| GET/POST | `/api/heartbeat/:token` | công khai | service báo còn sống |

Dashboard mặc định chạy ở cổng **2704** (`make dev-ui`, `make ui-start`), API ở 8787.

### Auth + tài khoản (đã làm)
| Method | Path | Quyền | Mô tả |
|---|---|---|---|
| GET | `/api/health` | public | Trạng thái + version |
| GET | `/api/auth/status` | public | `{has_users, default_admin}`: trang login gợi ý `admin` / `admin` khi tài khoản mặc định còn chưa thiết lập |
| POST | `/api/auth/setup` | tài khoản mặc định | `{email, name, password}`: đặt email và mật khẩu thật; trước đó mọi API khác trả 403 |
| POST | `/api/auth/login` | public | `{email, password}`, đặt cookie |
| POST | `/api/auth/logout` | public | Xóa phiên hiện tại |
| GET | `/api/auth/me` | đăng nhập | User hiện tại |
| POST | `/api/auth/password` | đăng nhập | `{old_password, new_password}`, thu hồi các phiên khác |
| GET | `/api/users` | admin | Danh sách tài khoản |
| POST | `/api/users` | admin | `{email, name, role, password}` |
| PATCH | `/api/users/:id` | admin | `{disabled}`; không tự vô hiệu hóa chính mình |
| POST | `/api/users/:id/reset-password` | admin | `{password}`, thu hồi mọi phiên |
| GET | `/api/audit?limit=` | admin | Audit log mới nhất |

### Config
| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/config` | Config hiện tại + `hash` |
| PUT | `/api/config` | Ghi toàn bộ, validate schema, cần `If-Match` |
| POST | `/api/config/diff` | So config gửi lên với hiện tại |
| GET | `/api/config/history` | Danh sách bản lưu |
| GET | `/api/schema` | JSON Schema để UI dựng form |

### Org + agent
| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/org` | Cây tổ chức + trạng thái |
| GET | `/api/agents/:id` | Chi tiết + thống kê |
| PATCH | `/api/agents/:id` | Sửa một phần config của agent (ghi vào file) |
| POST | `/api/agents/:id/run` | Chạy ngay |
| POST | `/api/agents/:id/enable` \| `/disable` | |
| GET | `/api/agents/:id/memory?layer=` | Memory hiện hành |
| GET | `/api/agents/:id/memory/history?layer=` | Các version |
| POST | `/api/agents/:id/memory/compact` | Ép compaction |
| GET | `/api/roles` | Danh sách role template |

### Job (`/jobs`)
| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/jobs?project=&cursor=` | Danh sách |
| GET | `/api/jobs/groups` | Gom theo từng việc (trang Job) |
| GET | `/api/jobs/stats?since=24h&project=` | Số liệu 24 giờ |
| GET | `/api/jobs/{id}` | Chi tiết |
| POST | `/api/jobs/{id}/cancel` \| `/retry` | |

### Cần xử lý (Tổng quan, ADR-051)
| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/incidents?status=` | Giám sát lỗi, tiến trình chết, automation tắt, bot mất kết nối, job lỗi 24h, thẻ chờ duyệt |
| POST | `/api/incidents/dismiss` \| `/retry` | Bỏ qua / thử lại 1 mục |
| GET | `/api/actions/pending` | Thẻ chờ duyệt (agent đề xuất chạy/dừng tiến trình, container — ADR-030) |
| POST | `/api/actions/{id}/approve` \| `/reject` | |
| POST | `/api/patches/{id}/approve` \| `/reject` | Duyệt diff agent sửa file (worktree) |
| POST | `/api/proposals/skip-all` | Bỏ qua mọi đề xuất đang chờ của 1 project |

### Thiết lập AI project (`/projects/:id/setup`, ADR-018)
| Method | Path | Mô tả |
|---|---|---|
| POST | `/api/projects/:id/setup/scan` | Quét tĩnh |
| POST | `/api/projects/:id/setup/propose` | Đề xuất mô hình tổ chức (gọi LLM) |
| POST | `/api/projects/:id/setup/build` | Dựng lại preview khi đổi lựa chọn |
| POST | `/api/projects/:id/setup/apply` | Áp dụng |

### Chi phí + trạng thái
| Method | Path | Mô tả |
|---|---|---|
| GET | `/api/usage/summary?days=` | Chi phí hôm nay, theo ngày, theo project/model (Tổng quan) |
| GET | `/api/usage/runs?...` | Lịch sử lượt gọi AI |
| PUT | `/api/usage/settings` | Trần chung office + giá model (trang Kết nối AI) |
| GET/PUT | `/api/projects/:id/budget` | Ngân sách riêng của project (tab Thông tin) |
| GET | `/api/system`, `/api/system/stats`, `/api/system/summary` | Trạng thái máy |

### Burn (đã làm, admin, ADR-087)

| Method | Path | Mô tả |
|---|---|---|
| GET/PUT | `/api/projects/:id/burn` | phiên + việc / lưu cài đặt |
| POST | `/api/projects/:id/burn/start` | cài đặt kèm `ends_at?`, `no_end?`; mặc định tắt lúc reset hạn mức tuần, không có thì sau 8 giờ |
| POST | `/api/projects/:id/burn/stop` | tắt, việc đang làm thành tạm dừng |
| POST | `/api/burn-items/:item/skip\|first\|drop-worktree` | thao tác trên một việc |

### Realtime (SSE, ADR-072, ADR-078)
Một kết nối `GET /api/events` mỗi tab. Event đầu tiên là `hello {sid}`.

| Event | Nội dung |
|---|---|
| `change` | `{tables}`: bảng vừa đổi, trang tự tải lại phần liên quan |
| `stats`, `machine` | số liệu máy (admin); `machine` chỉ khi bật chủ đề qua `POST /api/events/topics` |
| `message`, `conversation`, `conversation.deleted` | dữ liệu chat, gửi đúng người được xem |
| `incidents` | Cần xử lý của từng người |
| `mcp.status` | kết quả kiểm tra MCP (admin) |

Kết nối lại thì tải lại tất cả một lần.

### Webhook (không cho UI)
| Method | Path | Mô tả |
|---|---|---|
| POST | `/hooks/:source` | Verify theo `TriggerSource`, dedupe vào `events` |

## Nguyên tắc UI/UX

- Config là nguồn sự thật: mọi form hiện diff trước khi ghi và báo xung đột nếu file đã đổi (`412`).
- Evidence luôn click được. Kết luận không có evidence không được hiển thị như kết luận.
- Hành động side effect luôn cần xác nhận hai bước và hiện rõ tool + args sẽ chạy.
- Chi phí hiển thị ở mọi nơi có run (badge `$0.02`).
