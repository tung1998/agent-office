# Data model

Schema logic dùng chung cho SQLite và Postgres. Cột kiểu `json` là `jsonb` trên Postgres và `TEXT` (JSON hợp lệ, kiểm tra bằng `json_valid`) trên SQLite. `id` là ULID dạng text để sắp theo thời gian và không phụ thuộc sequence. Thời gian lưu UTC, ISO-8601 trên SQLite và `timestamptz` trên Postgres.

## Sơ đồ quan hệ

```mermaid
erDiagram
  agents ||--o{ memory_docs : has
  agents ||--o{ memory_entries : has
  agents ||--o| schedules : has
  agents ||--o{ runs : executes
  runs ||--o{ run_events : streams
  runs ||--o| worker_outputs : produces
  runs ||--o{ cost_ledger : costs
  threads ||--o{ findings : contains
  findings }o--o{ worker_outputs : "evidence (finding_evidence)"
  incidents ||--|| threads : discusses
  incidents ||--o{ approval_requests : requires
  events ||--o| runs : triggers
  budgets ||--o{ cost_ledger : limits
```

## Bảng

### `agents`
Mirror của config (ADR-011). Chỉ `office` ghi, khi sync từ file.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | = `id` trong config, VD `tech-lead` |
| level | text | `director` \| `manager` \| `worker` |
| role | text | tên role template |
| config | json | snapshot khối config của agent |
| config_hash | text | phát hiện thay đổi |
| enabled | bool | |
| disabled_reason | text null | `budget_daily` \| `failures` \| `manual` |
| disabled_at | ts null | |
| consecutive_failures | int | kill-switch |
| created_at, updated_at | ts | |

### `memory_docs`
Ba document mỗi manager/director (ADR-006). Mỗi lần ghi tạo version mới, không update tại chỗ.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| agent_id | text FK | |
| layer | text | `working` \| `long_term` \| `baseline` |
| version | int | tăng dần theo (agent_id, layer) |
| content | json | theo schema của layer, xem dưới |
| char_count | int | để kích hoạt compaction |
| source | text | `heartbeat` \| `compaction` \| `learn` \| `manual` |
| compacted_from | json null | danh sách `memory_entries.id` đã gộp |
| run_id | text null | run tạo ra version |
| created_at | ts | |

Unique `(agent_id, layer, version)`. Bản hiện hành = version lớn nhất.

**Schema content theo layer.**
- `working`: `{ mode, hypotheses: [{id, text, confidence, evidence_ids, opened_at}], open_questions: [], watchlist: [], last_checked_at, next_check_at, notes }`
- `long_term`: `{ summary, patterns: [{text, first_seen, last_seen, count}], past_incidents: [{incident_id, lesson}], noise: [{signature, reason}] }`
- `baseline`: `{ metrics: [{key, window, median, p95, stddev, by_hour?: {}, sample_size, computed_at}], interpretation }`

### `memory_entries`
Mục long_term được append trước khi compaction gộp vào document.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| agent_id | text FK | |
| kind | text | `pattern` \| `lesson` \| `noise` \| `fact` |
| text | text | |
| refs | json | id finding/incident/worker_output liên quan |
| superseded_by | text null | id version `memory_docs` đã gộp entry này |
| created_at | ts | |

### `schedules`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| agent_id | text FK | |
| kind | text | `heartbeat` \| `cron` |
| cron | text null | 5 trường, dùng khi `kind=cron` |
| timezone | text | |
| mode | text | `normal` \| `suspicious` \| `incident` |
| next_check_at | ts | index |
| last_checked_at | ts null | mốc delta cho worker |
| clean_streak | int | đếm để hạ mode |
| task | text null | nội dung run cron, VD `morning_report` |
| enabled | bool | |

### `events`
Inbox webhook và tin nhắn inbound, dedupe + debounce.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| source | text | `sentry` \| `graylog` \| `discord` \| … |
| dedupe_key | text | unique trong cửa sổ `dedupe_ttl` |
| payload | json | đã verify, raw giữ nguyên |
| severity | text null | map từ source |
| status | text | `received` \| `debounced` \| `dispatched` \| `ignored` |
| process_after | ts | debounce |
| run_id | text null | run được tạo |
| received_at | ts | |

### `runs`
Queue kiêm lịch sử và audit (ADR-004).

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| agent_id | text FK | |
| parent_run_id | text null | VD analyze run → plan run |
| trigger | text | `schedule` \| `cron` \| `event` \| `ask` \| `debate` \| `worker_request` \| `question` \| `compaction` |
| thread_id, incident_id | text null | |
| status | text | `pending` \| `running` \| `done` \| `failed` \| `skipped` \| `cancelled` |
| priority | int | incident > ask > schedule |
| attempt, max_attempts | int | |
| available_at | ts | retry backoff, debounce |
| lease_until | ts null | worker giữ run |
| input | json | context đầu vào (tham chiếu, không nhét raw lớn) |
| prompt_hash | text | để so sánh, không lưu prompt đầy đủ mặc định |
| output | json null | output đã validate |
| error, failure_class | text null | `runtime_unavailable` \| `quota` \| `timeout` \| `output_contract` \| `tool_surface` \| `budget` \| `internal` |
| runtime_used, model_used | text null | |
| fallback_index | int | 0 = runtime chính |
| session_id | text null | |
| input_tokens, cache_read_tokens, cache_write_tokens, output_tokens | int | |
| cost_usd | numeric | |
| cost_estimated | bool | |
| num_turns | int | |
| created_at, started_at, finished_at | ts | |

Index `(status, available_at, priority)`. Claim: Postgres `FOR UPDATE SKIP LOCKED`, SQLite `UPDATE … WHERE id = (SELECT … LIMIT 1) RETURNING *` trong `BEGIN IMMEDIATE`.

### `run_events`
Stream event từ adapter, giới hạn 1000 dòng mỗi run.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| run_id | text FK | |
| seq | int | PK cùng run_id |
| type | text | `text` \| `tool_call` \| `tool_result` \| `system` \| `error` |
| data | json | cắt ở 8 KB |
| at | ts | |

### `worker_outputs`
Output chuẩn của worker, là **evidence** duy nhất hợp lệ.

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| request_id | text | id yêu cầu từ manager, unique |
| run_id | text FK | |
| worker_id | text FK | |
| requested_by | text | agent id manager/director |
| query | text | câu hỏi/tham số đã chuẩn hóa |
| source | json | `{server, tool, args}` |
| data | json | dữ liệu có cấu trúc đã cắt gọn |
| raw_ref | text | `db:<blob_id>` hoặc `file:.office/blobs/<id>` |
| raw_bytes | int | |
| summary | text | tối đa 500 ký tự, không kết luận |
| delta_from, delta_to | ts null | khoảng dữ liệu |
| timestamp | ts | thời điểm lấy |

### `blobs`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| content | bytea / blob | ≤ 1 MB, gzip |
| sha256 | text | dedupe |

### `threads`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| title | text | |
| origin | text | `heartbeat` \| `event` \| `ask` \| `report` |
| status | text | `open` \| `debating` \| `closed` |
| debate_phase | text null | `independent` \| `cross_review` \| `synthesis` |
| round | int | |
| created_by | text | agent id hoặc `human:<name>` |
| created_at, closed_at | ts | |

### `findings`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| thread_id | text FK | |
| author | text | agent id |
| kind | text | `observation` \| `hypothesis` \| `question` \| `rebuttal` \| `conclusion` |
| round | int | 0 = independent |
| body | text | markdown ngắn |
| confidence | real | 0..1, bắt buộc trừ `question` |
| reply_to | text null | finding bị phản biện/trả lời |
| addressed_to | text null | cho `question` |
| severity | text null | `info` \| `low` \| `medium` \| `high` \| `critical` |
| run_id | text FK | |
| created_at | ts | |

### `finding_evidence`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| finding_id | text FK | PK cùng worker_output_id |
| worker_output_id | text FK | |
| note | text null | trích đoạn liên quan |

Ràng buộc evidence (ADR-007) enforce ở `blackboard.Post` trong transaction: `kind in (observation, hypothesis, rebuttal, conclusion)` phải có ≥ 1 dòng ở đây.

### `incidents`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| thread_id | text FK unique | |
| title | text | |
| severity | text | |
| status | text | `open` \| `debating` \| `awaiting_approval` \| `resolved` \| `dismissed` |
| participants | json | agent ids |
| conclusion_finding_id | text null | |
| summary | text null | tóm tắt cho người |
| options | json null | `[{id, title, description, side_effect: bool, risk, approval_request_id?}]` |
| confidence | real null | |
| risks | json null | |
| truncated | bool | debate bị cắt do giới hạn |
| token_used, cost_usd | int, numeric | |
| opened_at, resolved_at | ts | |

### `approval_requests`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| incident_id | text FK null | |
| requested_by | text | agent id |
| action | json | `{worker_id, tool, args, description}` |
| status | text | `pending` \| `approved` \| `rejected` \| `expired` \| `executed` \| `failed` |
| decided_by | text null | người duyệt |
| decision_note | text null | |
| expires_at | ts | |
| executed_run_id | text null | |
| created_at, decided_at | ts | |

### `budgets`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| scope | text | `org` \| `agent` \| `incident` |
| scope_id | text | `*` cho org |
| period | text | `day` \| `incident` |
| limit_usd | numeric | |
| max_runs_per_hour | int null | |
| action_on_exceed | text | `skip` \| `disable` \| `downgrade_model` |

PK `(scope, scope_id, period)`. Sinh từ config khi sync: `budget.*` cho scope `org`/`incident`, `rules.daily_cost_limit_usd` và `rules.max_runs_per_hour` của từng agent cho scope `agent`.

### `cost_ledger`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| day | date | theo timezone project |
| agent_id | text | |
| incident_id | text null | |
| run_id | text FK | |
| runtime, model | text | |
| input_tokens, cache_read_tokens, cache_write_tokens, output_tokens | int | |
| cost_usd | numeric | |
| cost_estimated | bool | |

Index `(day, agent_id)`, `(incident_id)`. View `cost_daily` group theo `day, agent_id` cho UI.

### `users`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | `usr_<ulid>` |
| email | text unique | lowercase, so sánh không phân biệt hoa thường |
| name | text | |
| role | text | `admin` (cả office) \| `manager` (quản lý project của mình) \| `member` (ADR-224) |
| password_hash | text | argon2id encoded |
| disabled | bool | |
| discord_id, telegram_id | text | user ID trên nền tảng bot ('' = chưa gắn); một ID một tài khoản. Tin gửi bot từ ID đó hiện tên tài khoản (`messages.sender`) |
| created_at, updated_at, last_login_at | ts | |

### `project_members`
Project mà `manager`/`member` thấy (ADR-224); `admin` thấy mọi project.
| Cột | Kiểu | Ghi chú |
|---|---|---|
| project_id | text FK | `repos`, cascade delete |
| user_id | text FK | `users`, cascade delete |
| created_at | ts | |

### `sessions`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | `ses_<ulid>` |
| user_id | text FK | cascade delete |
| token_hash | text unique | SHA-256 của token, không lưu token gốc |
| created_at, expires_at, last_seen_at | ts | TTL 7 ngày, idle 24 giờ |
| user_agent, ip | text | |

### `providers`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | `prv_<ulid>` |
| name | text unique | |
| kind | text | `anthropic` \| `openai` \| `openai_compatible` \| `claude_cli` \| `codex_cli` \| `gemini_cli` \| `antigravity_cli` (không còn CHECK, Go kiểm tra) |
| base_url | text | URL API, hoặc đường dẫn binary với CLI |
| api_key_enc | text | AES-256-GCM, không bao giờ trả qua API |
| api_key_env | text | đọc key từ biến môi trường này |
| api_key_hint | text | 4 ký tự cuối |
| tier_models | json | `{strong, balanced, fast}` |
| limit_caps | json | `{five_hour, seven_day}` → % dừng nhận lượt mới (ADR-136) |
| cli_home | text | Thư mục cấu hình của kết nối CLI đăng nhập tài khoản riêng (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GEMINI_CLI_HOME`); rỗng = tài khoản của máy (ADR-178, ADR-179) |
| models | json | danh sách model lần kiểm tra cuối |
| is_default, enabled | bool | |
| status, status_detail, checked_at | text, text, ts | `unknown` \| `ok` \| `error` |

### `repos`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | `rep_<ulid>` |
| name, description | text | tự nhận từ package.json, composer.json, go.mod |
| path | text unique | đường dẫn tuyệt đối trên máy chạy office |
| git_remote | text | đã bỏ thông tin đăng nhập trong URL |
| default_agent_id | text | agent trả lời khi không ai được gọi tên ('' = agent đầu tiên đang bật) — ADR-099 |

### `agents`
| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | `agt_<ulid>` |
| project_id | text FK | `repos.id`, cascade (ADR-099; trước là `org_model_id`) |
| key | text | unique trong project |
| name, role, description | text | |
| provider_id | text FK null | null = kết nối mặc định; set null khi xóa kết nối |
| fallback_provider_ids | json | kết nối thử tiếp theo thứ tự |
| model_tier | text | `strong` \| `balanced` \| `fast` |
| llm_model, effort | text | model cụ thể (ghi đè hạng), mức suy nghĩ |
| instructions | text | system prompt riêng |
| permissions | json | gói quyền, caps, lệnh, `full_access` (ADR-074) |
| avatar | json | |
| enabled | bool | false = tạm dừng |
| sort | int | |

### `revisions`
Ảnh chụp agent của một project trước mỗi thay đổi (giữ 50): `project_id`, `action`, `actor`, `agent_count`, `snapshot` (`{default, agents[]}`).

### `workflows` / `workflow_runs` (ADR-098)
`workflows`: bản quy trình của project: `project_id`, `key` (unique trong project), `name`, `description`, `source` (cả file), `source_key`/`source_hash` (mẫu thư viện đã chép), `bindings` (vai → agent_id), `enabled`.

`workflow_runs`: một lần chạy: `project_id`, `conversation_id` (chat riêng của lần chạy, ADR-101), `caller_conversation_id` (chat đã gọi; '' = lần chạy cũ), `parent_run_id`, `depth` (quy trình con, ADR-102), `outputs` (đầu ra theo key, ADR-103), `workflow_id`, `workflow_key`, `coordinator_id`, `input`, `status` (`running` \| `done` \| `failed` \| `stopped`), `turns`, `cost_usd`, `roles` (json: vai, agent, phiên, round, trạng thái, chi phí), `gates`, `log`, `result`, `error`, `started_at`, `finished_at`.

### `agent_skills` (ADR-185)

Skill mỗi agent giữ sẵn: `agent_id` (FK `agents`, cascade) + `name` là khóa, `why` (khi nào dùng), `source` (`person` | `agent` | `setup` | `library` | `call`), `uses`, `last_used_at` ('' = chưa dùng), `created_at`. Tối đa 12 mỗi agent (`agentskills.Limit`). Prompt chỉ có tên, mô tả, lý do, số lần dùng; hướng dẫn đọc bằng `use_skill`. Agent đề xuất cài skill của thư viện office bằng thẻ `actions.kind = install_skill` (ADR-187).

### `audit_log`

| Cột | Kiểu | Ghi chú |
|---|---|---|
| id | text PK | |
| actor | text | agent id, `human:<name>`, `system` |
| action | text | `run.start`, `run.finish`, `approval.decide`, `config.write`, `agent.disable`, … |
| target | text | |
| detail | json | |
| at | ts | |

Append-only. Không có API update/delete.

### `burn_sessions` / `burn_items` (ADR-087)

Mỗi project có một phiên Burn (`project_id` UNIQUE): `conversation_id` (chat `purpose = burn`), `agent_id`, `model_tier`, `max_parallel` (số việc làm cùng lúc, ADR-117), `run_branch` (nhánh `burn/<ngày-giờ>` của lần chạy, worktree `burn-run-<ngày-giờ>` nằm trên nó, ADR-123; cột `result_mode` cũ không còn đọc), `focus`, `ends_at` (null = không hẹn), `state` (`running` | `stopped` | `waiting_limit` | `draining`: làm nốt việc dở rồi tắt, ADR-118), `waiting_until`, `started_by`, `scanned` (vùng các lượt quét đã xem, ADR-116), `code_map` (bản đồ code lượt quét giữ cho lượt sau và worker, chỉ ghi qua `SetCodeMap`, ADR-130), `verify` (lệnh kiểm chứng, mỗi dòng một lệnh; rỗng: tự đoán), `lessons` (sổ bài học, chỉ ghi qua `SetLessons`, ADR-131), `review_cap`, `stop_after` (chờ review khi đủ N việc chưa merge, tắt sau N việc xong; 0: không giới hạn, ADR-135), `notify_channel_id` + `notify_chat_id` (bot và chat nhận tổng kết khi dừng, ADR-120), `run_plan` (JSON: chạy tới đâu mỗi lần bật, lịch tự bật; ADR-149).

Việc của phiên: `title`, `kind` (`unfinished` | `upgrade` | `bug` | `idea`; `quest`: người dùng giao, ADR-129; rỗng: một lượt quét, ADR-130), `detail`, `status` (`found` | `queued` | `doing` | `paused` | `done` | `failed` | `skipped`), `priority`, `branch`, `worktree`, `summary`, `subagents`, `cost_usd`, `work_conversation_id` (chat làm việc ẩn, `purpose = burn_work`, ADR-116), `run_branch` (lần chạy của việc; rỗng: quest chưa làm, ADR-129), `files` (file việc đã đổi, lấy lúc gộp, ADR-131).

### `chat_queued` (ADR-119)

Tin nhắn viết trong lúc chat đang trả lời: `conversation_id` (xóa theo chat), `author`, `text`, `attachments` (JSON tham chiếu file), `context` (trang đang mở), `options` (JSON `mode`, `edit_mode`, `agent_id`, `effort` áp khi gửi), `created_at`. Chat rảnh thì office gửi cả hàng thành một tin rồi xóa.

### `collections` / `records` / `record_history` (ADR-172)

`collections`: một bảng dữ liệu của project: `project_id`, `key` (unique trong project), `name`, `description`, `pack` (gói đã khai báo; rỗng: project tự tạo), `fields` (JSON: `name`, `label`, `type`, `options`, `ref`, `required`, `unique`, `indexed`).

`records`: `collection_id`, `project_id`, `data` (JSON theo schema), `text` (các trường chữ, cho `records_fts` tìm không dấu), `created_by`, `updated_by`. Trường `indexed`/`unique` có expression index `json_extract(data, '$.<field>')`; `unique` là index UNIQUE riêng từng collection.

`record_history`: mỗi thay đổi một dòng: `record_id`, `op` (`create` | `update` | `delete`), `data` (sau khi tạo/sửa, trước khi xóa), `by`, `at`. Chỉ thêm.

## Khác biệt SQLite / Postgres

| Điểm | SQLite | Postgres |
|---|---|---|
| JSON | `TEXT` + `CHECK(json_valid(col))`, truy vấn bằng `json_extract` | `jsonb`, index GIN khi cần |
| Claim queue | `BEGIN IMMEDIATE` + `UPDATE … RETURNING` | `FOR UPDATE SKIP LOCKED` |
| Đồng thời | WAL mode, `busy_timeout=5000`, một writer | Nhiều writer |
| Blob | `BLOB` | `bytea` |
| Numeric cost | `REAL` (đủ cho USD 6 chữ số) | `numeric(12,6)` |
| Realtime UI | poll `run_events` theo seq | có thể dùng `LISTEN/NOTIFY` |

## Retention

- `run_events`: 14 ngày.
- `blobs` của `worker_outputs` không còn được finding tham chiếu: 30 ngày.
- `runs`, `findings`, `incidents`, `cost_ledger`, `audit_log`: giữ vĩnh viễn (dữ liệu cho `tune`/`learn`).
