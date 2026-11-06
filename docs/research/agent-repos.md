# Kho GitHub: ai-agents-for-beginners, GenAI_Agents, Hands-On-AI-Engineering, HF Agents Course

Nguồn (chỉ đọc README/mục lục, không clone):
- https://github.com/microsoft/ai-agents-for-beginners (README, bảng bài học)
- https://github.com/NirDiamant/GenAI_Agents (README, danh sách `all_agents_tutorials/`)
- https://github.com/Sumanth077/Hands-On-AI-Engineering (README, danh mục `ai_agents/`)
- https://huggingface.co/learn/agents-course/unit0/introduction (trang giới thiệu + mục lục chương)

## Thư viện mẫu của office nạp thế nào

`internal/automation/library.go` là nơi office giữ thư viện skill/agent/mcp/automation dùng lại được:
- `Library.List` (`internal/automation/library.go:71`) liệt kê một loại (`skill`, `agent`, `mcp`, `template`) từ `<office home>/library/<skill|agents|mcp|automation-templates>`.
- `Library.Get` (`internal/automation/library.go:231`) nạp một mục, bao gồm nội dung file.
- `AutomationPreset` (`internal/automation/library.go:45`) là mẫu tự động hóa dùng lại được (không kèm agent/project cụ thể).
- Các **mẫu agent có vai trò + workflow đầy đủ** (không chỉ 1 file skill) nằm ở `templates/packs/*/office.pack.json`, nạp qua `templates/packs/packs.go` (biến `files embed.FS`, dòng 26).

## Bảng đối chiếu

| Mẫu | Nguồn | Office đã có, file:dòng | Thiếu | Lợi ích |
|---|---|---|---|---|
| Multi-agent nhóm nghiên cứu (Lead/Scout/Analyst/Verifier) | ai-agents-for-beginners `08-multi-agent`; GenAI_Agents "Research Team (AutoGen)"; Hands-On-AI "Research Team" | `templates/packs/research/office.pack.json:15` (research-lead), `:34` (research-scout), `:53` (research-analyst), `:65` (research-verifier); workflow `:84` (#deep-research), `:94` (#fact-check), `:102` (#market-scan), `:111` (#weekly-digest) | — | — |
| Planner → Coder → Reviewer (coding team) | Hands-On-AI "Multi-Agent Coding Assistant" | `templates/packs/webapp/office.pack.json:15` (web-lead), `:37` (web-developer), `:49` (web-reviewer), workflow `:78` (build-feature), `:87` (fix-bug); tương tự `templates/packs/mobileapp/office.pack.json:15,37,49,78,87` | — | — |
| Agentic RAG trên dữ liệu riêng | ai-agents-for-beginners `05-agentic-rag`; GenAI_Agents "Controllable RAG Agent", "Art Tourguide with LightRAG"; Hands-On-AI "Self-Reflective Agentic RAG" | `internal/cognee/cognee.go:84` (`Client.Recall`, tìm trên wiki qua Cognee khi >100 tài liệu), `:57` (`Client.Remember`); `internal/embed/index.go:60` (`Index.Run`, đánh chỉ mục embedding định kỳ), `:75` (`Index.All`) | — | — |
| Memory-augmented agent | GenAI_Agents "Memory-Enhanced Conversational Agent" | `internal/memory/memory.go:494` (`Recall`, lấy trí nhớ liên quan theo chủ đề), `:297` (`Service.Compact`), `:443` (`Topics`) | — | — |
| Human-in-the-loop approval | GenAI_Agents "Human-in-the-Loop Approval Agent" | `internal/officetools/officetools.go:294` (tool `propose_action`), `:469` (tool `decide_request`), `:578` (`decide_request`/`pending_requests` chỉ chạy được khi `sc.Admin`, ADR-188) | — | — |
| Trust & safety / bảo mật agent | ai-agents-for-beginners `06-building-trustworthy-agents`, `18-securing-ai-agents` | `internal/storage/org.go:101` (struct `Permissions`: `Level`, `Caps`, `Commands`, `Processes`, `Containers`, `RequiresApproval`, `FullAccess`) | — | — |
| Customer support routing | Hands-On-AI "Customer Query Routing Agent" | `templates/packs/support/office.pack.json:15` (support-lead), `:33` (support-replier), `:45` (support-checker), workflow `:63` (triage-tickets), `:89` (escalate) | — | — |
| Reflection / self-critique (vòng viết → tự chấm → sửa) | ai-agents-for-beginners `09-metacognition`; GenAI_Agents "Reflection From Scratch", "Self-Improving Agent" | Có một phần: mẫu pipeline review→verify trong Workflow tool (mô tả trong schema của tool `Workflow` ở phần hệ thống, không phải file trong `templates/`: mỗi "dimension" review xong lập tức verify) | Chưa có **mẫu agent/skill dùng lại được** trong `templates/library/skills/` cho vòng lặp tự-phê-bình đơn (viết → tự chấm theo rubric → sửa) ngoài review code | Rút ngắn: kịch bản 1 agent tự kiểm chất lượng bài viết/báo cáo trước khi gửi người dùng, không cần cả workflow review-verify nặng |
| Trace-based agent evaluation / observability | GenAI_Agents "Trace-Based Agent Evaluation"; HF Agents Course Bonus Unit 2 "Agent Observability and Evaluation" | Có một phần: tool `monitor_detail`, `ops_overview` (hệ thống, giám sát tiến trình/automation đang chạy, không phải chất lượng đầu ra agent) | Chưa có cách **so sánh chất lượng đầu ra của một agent/skill qua các lần sửa prompt** trong `templates/library/` (chạy lại vài transcript thật, chấm theo rubric, biết sửa có làm tệ đi không) | Khi sửa prompt agent trong `templates/library/`, biết ngay có hồi quy không thay vì chỉ đoán |
| Thiết kế tool cho agent (tên rõ, lỗi agent sửa được) | GenAI_Agents "Designing Tools for Agents" | Có rải rác: mỗi mẫu MCP trong `internal/automation/library.go:39` (`LibraryItem.Template *MCPTemplate`) tự viết `Description` riêng, không có chuẩn chung ghi thành tài liệu | Chưa có **hướng dẫn ngắn** (README) khi thêm MCP template mới vào thư viện | Tool mới vào thư viện MCP sẽ có tên/lỗi agent dùng được ngay, đỡ phải sửa lại sau |

## Đề xuất (3) — đã ghi quest bằng `burn_add(kind="idea")`

(Burn ghi việc của chính nó bằng `burn_add`, không phải `burn_quest`: `internal/officetools/officetools.go:572` — "a Burn records its own work with burn_add".)

1. **bit_01m6s8mmdahsrpfe6rrrz8f2h3** — Thêm skill tự-phê-bình một lượt (self-critique) cho báo cáo/bài viết: viết nháp → tự chấm theo rubric 3-5 tiêu chí ghi sẵn trong skill → sửa một lần, dành cho một agent đơn (nhẹ hơn workflow review→verify sẵn có). Cách kiểm: chạy thử trên 1 báo cáo thật, so bản có/không dùng skill, agent review phải chỉ ra ít nhất 1 lỗi bản kia bỏ sót.
2. **bit_01m6s8msapv5ky3ggm9md1y6ha** — Thêm skill "trace-based agent eval": khi sửa prompt một agent/skill trong `templates/library/`, chạy lại 3-5 transcript thật (qua `burn_list`/`read_link`/`search_history`), so đầu ra trước/sau theo rubric ngắn. Cách kiểm: áp cho 1 agent sửa gần đây (vd research-lead), chỉ ra cụ thể bản mới khác bản cũ ở điểm nào.
3. **bit_01m6s8mxfy0e2spd4w9d4kxwfx** — Thêm `templates/library/mcp/README.md` (file mới): hướng dẫn ngắn tên tool rõ hành động, mô tả khi nào dùng/không dùng, lỗi trả về agent sửa được. Cách kiểm: áp vào 1 MCP template có sẵn, chỉ ra ít nhất 1 chỗ cần sửa theo hướng dẫn.
