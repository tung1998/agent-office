# Sách: AI Engineering, AI Agents Guide, Agents with MCP, LLM Eng Handbook…

Nguồn (sách có phí): chỉ mục lục/giới thiệu nhà xuất bản, repo code đi kèm, review công khai — không tìm bản lậu.

- **AI Engineering** (Chip Huyen, O'Reilly) — https://www.oreilly.com/library/view/ai-engineering/9781098166298/ — mục lục chính thức O'Reilly.
- **AI Agents: The Definitive Guide** (Nicole Koenigstein) — mô tả/mục lục nhà xuất bản (link rút gọn LinkedIn).
- **Building Applications with AI Agents** (Michael Albada) — mô tả/mục lục nhà xuất bản.
- **AI Agents with MCP** (Kyle Stratis) — mô tả/mục lục nhà xuất bản + repo code mẫu MCP.
- **The LLM Engineering Handbook** — mô tả/mục lục nhà xuất bản (LLMOps/MLOps cho LLM).
- **Building an LLM from Scratch** — nền tảng (pretraining/tokenizer), không liên quan trực tiếp office.
- **Understanding Deep Learning** (Simon Prince, sách mở https://udlbook.github.io/udlbook/) — nền tảng toán/DL, không liên quan trực tiếp office.

## Bảng

| Sách | Chương/ý liên quan agent-office | Office đã có, file:dòng | Thiếu |
|---|---|---|---|
| AI Engineering | Ch. Evaluation (eval tự động, golden set, LLM-as-judge); Ch. RAG & agents; Ch. cost/latency trade-off (model routing, batching) | `internal/burn/review.go:24` review 3 tầng (issue/plan/result); `internal/errdigest` gộp lỗi; `internal/budget/README.md` guard chi phí theo org/agent/incident, rate limit, kill-switch; model routing qua `internal/llm/tiers.go` | Không có golden set cố định để chấm tự động trước/sau khi đổi prompt/model (đã ghi ở `docs/research/agent-eval-mcp.md`, quest `bit_01m6s983zbb5zvzdsvs7pbbmtg` đã có sẵn) |
| AI Agents: The Definitive Guide | Agent patterns (planner/executor, tool use), multi-agent orchestration, memory | `internal/memory/memory.go` 2 tầng core/topic notes (ADR-068/134); `internal/orchestrator`, `internal/debate` (state machine independent→cross_review→synthesis, ADR cho M2); `internal/actor`, `internal/burn` pick/review | Không thấy pattern "planner tách khỏi executor" rõ ràng — Burn gộp cả việc tìm + sửa trong một piece; đây là khác biệt thiết kế có chủ đích (burn-bit tự chọn việc), không phải thiếu sót cần sửa |
| Building Applications with AI Agents | Guardrails, human-in-the-loop approval, production hardening | `internal/mcpgateway/policy.go:72` `decide()` readOnly/trusted→pass, còn lại→propose/deny theo tool; `propose_action`/`decide_request` (approval card cho người); `internal/audit` | Không có "simulation/dry-run" trước khi agent chạy hành động nhạy cảm lần đầu trên dữ liệu thật (chỉ có propose, không có sandbox replay) |
| AI Agents with MCP | 3 primitive MCP (tools/resources/prompts), server nhỏ gọn một việc, approval theo rủi ro | `internal/mcpgateway/check.go:169` chỉ đọc `tools/list`; `storage.MCPServer.Agents` gán server theo agent/project | Không đọc `resources/list`, `resources/read`, `prompts/list` (đã ghi ở `docs/research/agent-eval-mcp.md`, quest `bit_01m6s98d7s26ntcg1zqj7qy255` đã có sẵn) — không lặp lại quest cũ ở đây |
| The LLM Engineering Handbook | LLMOps: versioning prompt, CI cho agent, monitoring production (latency/cost/error rate theo thời gian), feature store cho dữ liệu agent | `internal/monitor`, `internal/activity/failures.go:17` `failureDigest`; `internal/budget` ghi `cost_ledger`; ADR-263 trần chi phí theo cây job (root_job_id/root_automation_id) | Không có dashboard/theo dõi xu hướng cost+latency theo thời gian cho từng agent/model (có ledger thô, chưa có view xu hướng để phát hiện regression) |
| Building an LLM from Scratch | Nền tảng (tokenizer, pretraining, fine-tune) | — | Chỉ nền, bỏ qua |
| Understanding Deep Learning | Nền tảng toán/kiến trúc DL | — | Chỉ nền, bỏ qua |

## Đề xuất (tối đa 2)

1. **Dashboard xu hướng cost + latency theo agent/model theo thời gian** (từ LLM Engineering Handbook) — đáng làm: office đã ghi `cost_ledger` (`internal/budget`) và có ADR-263 trần chi phí theo cây job, nhưng không có nơi xem xu hướng để phát hiện một thay đổi (prompt/model) làm chi phí hoặc độ trễ tăng dần theo thời gian, chỉ có trần chặn khi vượt. Đã thêm quest bằng `burn_add`.
2. **Dry-run/simulation cho hành động nhạy cảm lần đầu** (từ Building Applications with AI Agents) — cân nhắc nhưng **không thêm quest**: office đã có `propose_action`/`decide_request` chặn trước khi chạy thật (người duyệt), gần tương đương mục tiêu "không để agent tự ý làm hỏng"; thêm một lớp dry-run riêng là trùng lặp cơ chế hiện có hơn là lấp khoảng trống thật.

Quest đã thêm lượt này: xem mục 1 (dashboard cost/latency). Hai quest về golden set và MCP resources đã có từ trước (`docs/research/agent-eval-mcp.md`), không tạo lại.
