# RAG Survey: truy hồi cho trí nhớ và wiki của office

Nguồn: "Retrieval-Augmented Generation for Large Language Models: A Survey", arXiv 2312.10997 (https://lnkd.in/gGUqkkyR).

## Office hiện lấy kiến thức thế nào

- **Notes (memory)**: hai tầng core/topic, đọc bằng tool `recall` → `memory.Recall` (`internal/memory/memory.go:494-534`) ghép `topic+summary+text`, lọc theo `textrank.Words`/`textrank.Rank` (`internal/textrank/textrank.go:28-90`, TF‑IDF kiểu BM-nhẹ trên từ đã bỏ dấu).
- **Truy hồi theo nghĩa (tùy chọn)**: nếu `COGNEE_URL` được cấu hình, `recallRelated` (`internal/officetools/officetools.go:1404-1414`) gọi Cognee lấy câu "liên quan theo nghĩa" của mọi agent trong project, 10s timeout, im lặng khi lỗi. Không bật ở project này theo mặc định (ADR-195).
- **Wiki**: `recallWiki` (`internal/officetools/officetools.go:1416-1433`) → `wiki.Search` (`internal/wiki/wiki.go:35-56`) chia `docs/wiki/*.md` theo heading, đoạn dài > 1500 ký tự cắt tiếp theo đoạn văn (`wiki.go:92-121`), rồi rank bằng `textrank` như notes.
- **search_history**: FTS5 thật (`internal/storage/sqlite/chat.go:594-632`, bảng `messages_fts`), mỗi từ bắt buộc có mặt (AND), từ cuối cho prefix, trả snippet 24 token quanh từ khớp.
- **Đọc code đầu lượt**: agent tự đọc file bằng Read/Grep, không qua tầng truy hồi nào — toàn bộ nội dung file vào context.

## Bảng đối chiếu

| Kỹ thuật (RAG survey) | Office đã có (file:dòng) | Thiếu | Lợi ích nếu thêm |
|---|---|---|---|
| Chia đoạn (chunking) | Có, cho wiki: cắt theo heading rồi theo đoạn văn, trần 1500 ký tự (`internal/wiki/wiki.go:92-121`) | Notes không chia đoạn — một "note" đã bị ép ≤500 ký tự khi ghi (`internal/memory/memory.go:64,97-99` `MaxNote`), nên luôn là 1 chunk | Nhỏ, không đo được — notes đã ngắn sẵn (≤500 ký tự ≈ 150 token), không có gì để chia nhỏ hơn mà tiết kiệm |
| Truy hồi lai từ khoá + embedding (hybrid) | Có khung: `recall` luôn chạy từ khoá (textrank) trước, cộng thêm kết quả embedding của Cognee nếu bật (`officetools.go:1026-1036`) | Cognee tắt mặc định ở phần lớn project (cần `COGNEE_URL`/container riêng) nên trong thực tế office chỉ chạy nhánh từ khoá | Không đo được ở project này (Cognee tắt, không có số thật); suy luận: mỗi lần agent phải gọi lại `recall` bằng từ khác vì trượt từ khoá tốn thêm một lượt gọi tool trọn vẹn (prompt system + notes lặp lại, cỡ vài nghìn token/lần) — hybrid giúp giảm số lần gọi lại đó, không giảm một lần gọi |
| Viết lại câu hỏi (query rewriting) | Không có | `recall`/`wiki.Search`/`search_history` dùng đúng từ query gốc, không sinh biến thể/đồng nghĩa trước khi rank | Ước lượng: mỗi lần tránh được một lượt `recall` trượt (phải gọi lại bằng từ khác) đỡ ~1 lượt tool call, tức vài trăm đến ~1-2 nghìn token input của riêng lượt gọi lại đó (không tính context nền đã cache) |
| Rerank | Không có | `textrank.Rank` chỉ xếp theo (số từ khớp, rồi idf cộng dồn) — không có bước rerank bằng model hay tín hiệu khác (độ mới, agent tạo ra) | Không đáng kể — danh sách recall đã ngắn (topic ≤4000 ký tự ≈ 1200 token), rerank thêm một lời gọi model (tối thiểu vài trăm token input+output) tốn hơn lợi nó mang lại |
| Nén ngữ cảnh (context compression) | Có dạng thô: cắt theo ký tự — `wiki.go:23` `snippet=400` rune mỗi hit, `search_history` cắt 24 token quanh từ khớp (`chat.go:620` `snippet(...,24)`), notes nén định kỳ bằng model khi vượt trần (`memory.go:60-109` `Compactor`) | Không có bước nén *lúc truy hồi* cho nhiều hit cùng lúc (vd tóm 5 hit wiki thành 1 đoạn) — mỗi hit vẫn nguyên 400 rune, 5 hit wiki + 8 dòng Cognee + notes core/topic đều vào prompt mỗi lần gọi `recall` | Ước lượng: 5 hit wiki × 400 rune ≈ 500 token; gộp còn 1-2 đoạn còn ≈ 150-200 token — tiết kiệm khoảng 300-350 token mỗi lần gọi `recall` có nhiều hit trùng ý |
| Đánh giá truy hồi (retrieval evaluation) | Không có | Không có test/metric đo recall@k hay "note có tìm thấy đúng lúc cần không" — chỉ có unit test đúng/sai của `textrank.Rank` chính nó (`internal/officetools/memory_test.go`) | Không đo token trực tiếp (đây là công cụ phát hiện lỗi, không tự giảm token) — giá trị là phát hiện sớm khi `MaxSummary=150`/`TopicLimit=4000` cắt mất note quan trọng, tránh agent phải dò lại nhiều lượt; cần hạ tầng đo mới, không phải việc nhỏ |

## Token mất vào đọc lại ngữ cảnh mỗi lượt

Từ bảng `jobs`/`runs` trong `.office/office.db` (7 ngày gần nhất, kind=`chat_turn`, status=`done`):

```
839 lượt · tổng input 2,723,533,757 token · tổng output 7,096,830 token
trung bình: 3,246,167 input / 8,459 output token mỗi lượt
```

Input trung bình mỗi lượt (~3.25M token) lớn hơn nhiều lần output (~8.5K) — phần lớn là context bị gửi lại mỗi lượt (system prompt, memory core+topic, skill, lịch sử hội thoại đã cache), không phải nội dung mới sinh ra. Đây đúng là chỗ RAG nhắm tới: thay vì nạp lại toàn bộ ngữ cảnh, chỉ truy hồi phần cần cho câu hỏi hiện tại. Ba kỹ thuật thiếu ở trên (query rewriting, nén nhiều-hit, rerank) đụng tới phần "truy hồi cho đúng, ít hit dư" chứ không giảm trực tiếp con số 3.25M này (số đó chủ yếu do cache ngữ cảnh hội thoại dài, nằm ngoài phạm vi RAG) — nên tác động thực tế là nhỏ/trung bình trên vài trăm–vài nghìn token mỗi lần gọi `recall`, không phải trên tổng 3.25M.

## Đề xuất

Chỉ 1 trong 3 chỗ thiếu đáng làm thành quest nhỏ, có file:dòng, có cách kiểm rõ:

1. **Chuẩn hoá viết tắt trước khi rank** (`textrank.Words`, `internal/textrank/textrank.go:28-36`): thêm bảng thay thế nhỏ (ko→không, dc/đc→được, vd→ví dụ...), khớp theo từ nguyên vẹn (không khớp bên trong một từ dài hơn, để không đụng thuật ngữ/tên biến chứa "dc"/"vd") trước khi fold, áp dụng cho cả recall notes, wiki và search_history (dùng chung `textrank.Words`). Cách kiểm: test mới (file mới, `internal/textrank` hiện chưa có `_test.go`) — note chứa "không có" phải được tìm thấy khi query là "ko có".
2. Nén nhiều-hit của wiki và Cognee: gộp khi >2 hit trùng path/heading. Lợi ích nhỏ (ước lượng ~300-350 token/lần gọi, xem bảng trên), không đáng một quest riêng — ghi lại ở đây để ai đó nhớ nếu sau này `recall` bị kêu dài.
3. Đánh giá truy hồi: cần hạ tầng (bộ câu hỏi mẫu + script đo recall@k), vượt quy mô "1 việc nhỏ" của quest — để dành cho một ADR nếu người dùng thấy recall thực sự trật nhiều.

→ Đã thêm 1 quest `[Tích hợp]` cho đề xuất 1 bằng `burn_add kind=idea` (không phải `burn_quest`: một piece Burn không có tool đó, bị lọc ra ở `internal/officetools/officetools.go:571-575` — "a Burn records its own work with burn_add"): **`bit_01m6s7phjmg5r1rbgqwxhsxt0c`** — "[Tích hợp] Chuẩn hoá viết tắt trước khi rank từ khoá (recall/wiki/search_history)". Đề xuất 2 và 3 không thêm quest: lợi ích đo được quá nhỏ (2) hoặc vượt quy mô một việc nhỏ, cần ADR riêng (3).
