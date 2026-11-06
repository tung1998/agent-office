# Đánh giá agent (eval) + xây agent với MCP

Nguồn:
- "Building and Evaluating AI Agents" — Sayash Kapoor (AI Snake Oil / Princeton), AI Engineer Summit 2025. Không lấy được transcript YouTube trực tiếp (id d5EItXhbcfA không mở được qua oEmbed); nội dung dựa trên bài báo đi kèm "AI Agents That Matter" (arXiv 2407.01502) mà bài nói dựng trên đó, và tóm tắt tại https://makiuto.github.io/p/building-and-evaluating-ai-agents-sayash-kapoor-ai-snake-oil.html.
- "Building Agents with Model Context Protocol" — Mahesh Murag (Anthropic), AI Engineer Summit. Transcript/tóm tắt qua https://tuananhbui89.github.io/blog/2025/llm-agents-lec06, https://lawwu.github.io/til/posts/2025-03-01-mcp-talk/index.html, https://ai.engineer/talks/building-agents-with-mcp.

## Eval

| Ý (video) | Office đã có, file:dòng | Thiếu | Lợi ích nếu làm |
|---|---|---|---|
| Vẽ đường cost-vs-accuracy (Pareto), không chỉ 1 số accuracy | Có chi phí mỗi run/Burn (`internal/burn/report.go`, `internal/llm/llm.go` lưu cost), nhưng không có "accuracy" vì không có bộ việc mẫu cố định để chấm đúng/sai | Không có bộ test case cố định (golden set) + điểm đúng/sai gắn với cost của cùng lần chạy | Biết đổi prompt/model có "đáng tiền" không, không chỉ "có vẻ tốt hơn" |
| Cảnh giác overfitting benchmark, dùng tập giữ lại | `internal/workflow/builtin/better-harness.md`, ADR-181/184 (`docs/DECISIONS.md:2604`) chẩn đoán vòng làm việc theo 5 mặt, nhưng chạy trên lịch sử thật đã xảy ra, không phải bộ test giữ lại | Không có "test set" độc lập với dữ liệu agent đã thấy | Tránh prompt được tối ưu khớp đúng các ca cũ nhưng vỡ với ca mới |
| Không tin số điểm 1 lần chạy (agent không ổn định) | `internal/activity/failures.go:17` `failureDigest` gộp lỗi theo nguyên nhân trên nhiều job thật; `internal/burn/review.go:24` 3 tầng review (issue/plan/result, ADR-112/113) | Không chạy lặp lại cùng 1 việc N lần để đo phương sai (agent có ổn định không) | Phát hiện agent "may mắn" một lần chứ không thật sự giỏi |
| Chuyển từ "có chạy được 1 lần" sang kỹ thuật độ tin cậy (deploy thật) | `internal/harness/harness.go`, `cmd/office/harness.go:15` (`office harness`) thu bằng chứng vòng làm việc thật, không giả lập | Không có cơ chế so sánh trước/sau một thay đổi (prompt, model, workflow) trên cùng bộ việc | Biết một thay đổi làm agent tốt lên hay tệ đi, thay vì đoán |

**Office đã có mà video không nói tới:** review 2 tầng do người hoặc agent khác chấm (`storage.BurnReviewStage`, `internal/api/burn_reviews.go`), và `failure_digest` gộp lỗi thật theo category (`internal/errdigest`) — gần với "reliability engineering" hơn là benchmark tĩnh.

**Khoảng trống chính:** không có bộ việc mẫu cố định (golden set, ví dụ 10-20 bit Burn đại diện các loại việc) để chấm tự động trước/sau khi đổi prompt/model/workflow, và gắn cost vào điểm đó. Hiện tại "tốt lên hay tệ đi" chỉ suy từ failure_digest + review của người, không có con số so sánh.

## MCP

| Ý (video) | Office đã có, file:dòng | Thiếu | Lợi ích nếu làm |
|---|---|---|---|
| 3 primitive: Tools (model gọi), Resources (app gắn dữ liệu cố định), Prompts (người gọi, slash command) | Chỉ dùng **Tools**: `internal/mcpgateway/check.go:169` đọc `tools/list`, `annotations.readOnlyHint` | Không đọc/dùng `resources/list`, `resources/read`, `prompts/list` của server MCP (grep xác nhận: không có "resources/list" hay "prompts/list" trong `internal/mcpgateway`) | Server MCP có resources (vd tài liệu cố định) hiện bị bỏ qua hoàn toàn; prompts (slash command từ server) không tới được người dùng |
| Chọn tool vs resource theo ai quyết định quyền truy cập (autonomy, cost, bảo mật) | — | Không áp dụng vì chưa đọc resources | Giảm gọi tool tốn token khi dữ liệu có thể lấy thẳng qua resource |
| Yêu cầu approval cho hành động nhạy cảm, không chỉ tin model | `internal/mcpgateway/policy.go:72` `decide()`: readOnly/trusted → pass; còn lại → `propose` (CanPropose) hoặc `deny`; theo từng tool (`ToolReadOnly`) | Đã có, khớp khá sát với khuyến nghị | — |
| Server nhỏ, gọn, một việc (vd GitHub server riêng, Asana server riêng, ghép lại) | `storage.MCPServer.Agents` (`internal/mcpgateway/policy.go:44` `Assigned`) cho phép gán server theo từng agent/project | Office đã làm đúng mô hình nhiều server nhỏ | — |
| Mô tả tool rõ ràng để model chọn đúng | `check.go:187` chỉ lưu `Description` nguyên văn từ server, không có bước kiểm tra/cảnh báo mô tả tool mơ hồ | Không có | Thấp — phụ thuộc server ngoài viết mô tả tốt, office không kiểm soát được |
| MCP không thay orchestration framework | Office tự có workflow/Burn riêng (`internal/workflow`), không dùng MCP để điều phối | Đã đúng hướng | — |

**Khoảng trống chính:** office mới nói chuyện với MCP server qua `tools/list` + `tools/call`; không đọc `resources/*` hay `prompts/*`. Với các MCP server chỉ cung cấp dữ liệu tĩnh qua resource (không cần model quyết định gọi hay không), office hiện phải mô phỏng bằng tool, tốn thêm một lượt gọi model.

## Đề xuất (tối đa 3)

1. **Bộ việc mẫu cố định (golden set) để so trước/sau** — đáng làm, lợi ích rõ (biết đổi prompt/model/workflow tốt lên hay tệ đi bằng số, không chỉ cảm tính). Đã thêm quest `bit_01m6s983zbb5zvzdsvs7pbbmtg` — "[Tích hợp] Bộ việc mẫu cố định (golden set) để so trước/sau khi đổi prompt/model" bằng `burn_add` (một piece Burn không có công cụ `burn_quest`: nó bị lọc khỏi danh sách tool của piece, `internal/officetools/officetools.go:571-573`, "a Burn records its own work with burn_add").
2. **Đọc MCP resources** (`resources/list`, `resources/read`) trong `internal/mcpgateway/check.go` bên cạnh `tools/list` — đáng làm vì là tính năng chuẩn MCP office hoàn toàn chưa chạm, dù nhỏ. Đã thêm quest `bit_01m6s98d7s26ntcg1zqj7qy255` — "[Tích hợp] Đọc MCP resources (resources/list, resources/read) trong mcpgateway" bằng `burn_add`.
3. **MCP prompts (slash command từ server)** — **không thêm quest**: lợi ích thấp cho office hiện tại (office đã có skill/slash command riêng của mình qua `use_skill`/`Skill`, không có nhu cầu rõ ràng là MCP server nào gắn vào office cần cấp prompt riêng). Để dành, không tốn công làm bây giờ.
