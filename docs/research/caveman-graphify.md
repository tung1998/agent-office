# caveman + graphify: thử giảm token agent code

Ngày kiểm: 2026-11-07. Quest: bit_01m6rqgmtapm8c3z50sf1dt72v. Tiếp nối phân loại đã ghi ở quest nhập skill (bit_01m6rqg8t1sk289y0n1mc7ryxy, commit a0fc9d24): "Không nhập (không phải repo skill, là CLI/app kèm wrapper skill, chỉ ghi lại): … safishamsi/graphify (thư viện/CLI Python), JuliusBrussee/caveman (khung CLI với generated/ cho nhiều agent)."

## 1. Chúng là gì, cài thế nào, chạy với Claude CLI/Codex không

### caveman (JuliusBrussee/caveman)
- Là gì: CLI (khung "generated/" cho nhiều agent) + skill đi kèm, không phải MCP server.
- Cài: `npx @caveman-ai/cli@latest` (cần Node 22.13+) **hoặc** chỉ cài skill bằng `npx skills add JuliusBrussee/caveman -g`, gọi qua `/caveman`. Cũng có bản `/plugin install caveman@caveman` cho Claude Code.
- Chạy với Claude CLI/Codex: README tự nhận hỗ trợ Claude Code, Codex, Gemini CLI, OpenCode, "30+ agents"; tính năng "Auto" (tự chọn model) chỉ ghi hỗ trợ Claude Code, OpenCode, Codex.
- Cơ chế: một bộ quy tắc hướng dẫn output (~1.160 token input) + nén output công cụ (log, JSON, CSV, kết quả test) trước khi đưa vào context model. README khẳng định không viết lại prompt người dùng.

### graphify (safishamsi/graphify → chuyển hướng, xem mục 2)
- Là gì: thư viện/CLI Python, không phải MCP bắt buộc (có extra `mcp` tùy chọn làm MCP stdio server).
- Cài: `uv tool install graphifyy` (PyPI package tên **`graphifyy`**, thêm một chữ "y" so với tên repo) rồi `graphify install`. Yêu cầu Python 3.10+.
- Chạy với Claude CLI/Codex: `graphify install` tự đăng ký skill `/graphify` + hook cho Claude Code (hook có thể chặn hẳn Read/Grep ở chế độ `--strict`); Codex dùng qua `$graphify` và `AGENTS.md`.
- Cơ chế: parse code bằng tree-sitter (~40 ngôn ngữ) ra `graph.json` một lần, sau đó agent query (`query`, `path`, `explain`) thay vì đọc lại file. Có extra `watch` để tự build lại khi file đổi; README không nói rõ xử lý trạng thái cũ khi graph lệch ngoài cờ `needs_update`.

## 2. Vì sao dừng, không cài — bằng chứng GitHub API thật

Gọi trực tiếp `api.github.com` (không qua README, không qua trang redirect):

| Repo | full_name thật | stars | forks | open issues | created_at | pushed_at |
|---|---|---|---|---|---|---|
| JuliusBrussee/caveman | JuliusBrussee/caveman | 117.999 | 6.860 | 69 | 2026-04-04 | 2026-11-06 |
| safishamsi/graphify | **Graphify-Labs/graphify** (301 Moved Permanently) | 143.508 | 13.725 | 1.646 | 2026-04-03 | 2026-11-06 |

Nhận xét:
- Cả hai repo mới tạo **~7 tháng** (04/2026) nhưng đã có **118k–143k sao** — trung bình trên dưới 550–680 sao/ngày liên tục trong 7 tháng cho một "khung CLI cá nhân" theo README caveman tự nhận "một người duy trì miễn phí". Đây là tốc độ không tự nhiên, dấu hiệu điển hình của star-farming (mua/bot sao) để tạo uy tín giả.
- `safishamsi/graphify` không còn tồn tại dưới tên đó: API trả 301 sang tổ chức khác hẳn (`Graphify-Labs`). Tên gói cài (`graphifyy`, thêm một chữ "y") khác tên lệnh (`graphify`) — mẫu hình typosquatting thường gặp.
- Cả hai đều có đường gửi dữ liệu ra ngoài máy: caveman CLI gửi telemetry mặc định (lệnh, token, install ID, IP) và tính năng "Auto" gửi nguyên văn câu hỏi gần nhất + nội dung CLAUDE.md/memory/file được `@` tới "Caveman Cloud"; graphify có lớp "Graphify Cloud" trả phí tương tự.

→ Không đủ tin cậy để cài CLI (`npx @caveman-ai/cli`, `uv tool install graphifyy`) hay hook `--strict` (có thể chặn Read/Grep) vào máy chạy việc thật. Graphify được xử lý như một ngoại lệ an toàn, bỏ qua bước cài/đo cho nó.

## 3. Đo thử — giới hạn kỹ thuật gặp phải

Dự định: chạy cùng một câu hỏi cố định 2 lần/cách (không dùng / có hướng dẫn kiểu caveman) qua một agent dự án thật, lấy job id + token input/output + chi phí từ bảng `runs`.

Thử bằng `mcp__office__delegate` (giao việc cho agent dự án "openAI") và `mcp__office__ask_expert`: cả hai đều báo lỗi **"chỉ giao việc được trong Chat"** / **"chỉ hỏi chuyên gia được trong chat của project (không phải bot, tự động hóa hay quy trình)"**. Một Burn piece chạy trong worktree riêng, không phải một Chat của project, nên cả hai đường tạo job/run thật trong `.office/office.db` đều bị khóa theo thiết kế (không phải lỗi tạm thời) — xác nhận lại ở vòng làm việc này, lỗi giống hệt lần trước.

Vì vậy bảng số liệu dưới đây là **proxy đo bằng tay trong phiên làm việc này** (không có job id thật trong `runs`), chỉ đo phần "trả lời gọn hơn" của caveman (phần duy nhất không cần cài gì — chỉ cần một dòng chỉ dẫn), KHÔNG đo graphify (không cài, xem mục 2):

Câu hỏi cố định: "Trong repo agent-office (Go), tool MCP `burn_add` được xử lý ở đâu (file:dòng), và nơi nào dispatch/gọi tới nó?"

Đã xác nhận bằng grep trong repo (file:dòng thật):
- Đăng ký tool: `internal/officetools/officetools.go:427`
- Dispatch (case "burn_add", …): `internal/officetools/officetools.go:906`
- Gọi handler thật `t.burn(...)`: `internal/officetools/officetools.go:908`
- Handler thật được gán ở: `cmd/office/run.go:324` (gọi `office.SetBurn(...)`, định nghĩa `SetBurn` ở `officetools.go:211`)

| Cách | Hướng dẫn thêm | Độ dài câu trả lời (ký tự) |
|---|---|---|
| Không dùng (baseline) | — | ~950 ký tự (giải thích đầy đủ 4 vị trí + vì sao, như trên) |
| Kiểu caveman (tự viết 1 dòng chỉ dẫn, không cài gì) | "Trả lời cực kỳ gọn: chỉ liệt kê file:dòng, không giải thích, không markdown thừa, dưới 60 từ." | ~180 ký tự: `officetools.go:427 (đăng ký) · officetools.go:906-908 (dispatch, gọi t.burn) · run.go:324 (SetBurn gán handler thật)` |

Giảm ~80% độ dài câu trả lời cho đúng loại câu hỏi "ở đâu/ai gọi" — tín hiệu output token giảm đáng kể nếu agent nào cần tra cứu nhanh kiểu này. Nhưng đây **không phải số liệu token/chi phí thật từ `runs`** như "Xong khi" yêu cầu — không lấy được từ môi trường Burn hiện tại (xem lỗi `delegate`/`ask_expert` ở trên).

## 4. Kết luận

- **graphify: không tích hợp.** Rủi ro dữ liệu/toàn vẹn (star-farming, typosquat tên gói, hook có thể chặn Read/Grep, Cloud trả phí) vượt lợi ích chưa kiểm chứng được. Không thêm quest tích hợp.
- **caveman (CLI đầy đủ + telemetry + Auto/Cloud): không tích hợp** vì cùng lý do uy tín/dữ liệu ở mục 2.
- **Chỉ riêng ý tưởng "trả lời gọn" (không cài gì, chỉ một dòng chỉ dẫn)** cho thấy tiềm năng giảm output token cho các câu hỏi tra cứu ngắn (vị trí hàm, ai gọi), dựa trên đo tay ở mục 3 — nhưng chưa có số liệu `runs` thật để kết luận chắc (giới hạn môi trường Burn), và quest này **không được đổi prompt hệ thống/cấu hình agent**.
- **Không thêm quest "[Tích hợp] Bật …"** vì chưa có bằng chứng đủ mạnh (thiếu số liệu `runs` thật) và nguồn gợi ý (caveman) không đáng tin để lấy cảm hứng cụ thể từ đó. Nếu người ra quest vẫn muốn theo hướng "trả lời gọn", nên mở một **Chat** bình thường (không phải Burn) để `delegate`/`ask_expert` ra số liệu `runs` thật trước khi quyết định thêm quest tích hợp.
