# Học ngoại ngữ

Repo học ngoại ngữ của một người, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/languages`. Repo giữ sổ học (bài học, bài tập, đề thi) dạng Markdown; từ vựng và ngữ pháp nằm trong bảng dữ liệu của project để lọc theo tag và lịch ôn.

## Luồng học

```
mục tiêu + ngày ──► lộ trình (roadmap/) + lịch học từng ngày (schedule) ◄── điều chỉnh hằng tuần
                    │
                    ▼
thêm từ mới ──► sổ: vocabulary, grammar (có tag) + lessons/<ngày>.md
                    │
                    ▼
ôn tập (hộp Leitner, next_review) ──► chấm ──► lên/xuống hộp, tag loi:*
                    │
                    ▼
đề theo mẫu kỳ thi (đã học + mẫu đề thật trên mạng) ──► chấm ──► điểm, phần yếu
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Lộ trình | "Mình muốn đỗ N3 trong 2 tháng, mỗi ngày 45 phút"; `#plan-roadmap`. Kiểm tra có kịp không, chia giai đoạn, tuần, mốc kiểm tra, xếp việc 2 tuần tới; hết giai đoạn phải qua ngưỡng đạt (khoảng 80% mục ở hộp 3 trở lên, mốc kiểm tra đạt) mới sang phần mới; lịch **Điều chỉnh lộ trình hằng tuần** sửa theo kết quả | Giáo viên lập, Chấm bài soát |
| Thêm từ mới | Gửi từ, câu, đoạn văn, ảnh sách, link vào chat; nhiều mục thì Giáo viên chạy `#add-words` | Giáo viên ghi, Chấm bài soát |
| Ôn tập | "Ôn bài" trong chat, hoặc lịch **Ôn tập hằng ngày**; `#review` | Soạn đề soạn, Giáo viên hỏi |
| Chấm | Trả lời trong chat; `#grade`. Từ chưa nhớ về hộp 1, sai nhiều thành **khó**, câu sai vào sổ **Lỗi hay sai**; buổi ôn sau dành khoảng 1/3 cho các mục yếu | Chấm bài |
| Luyện kỹ năng | Đọc (bài đọc đúng trình độ), nghe (link audio thật có transcript, chép chính tả, shadowing), nói (nhập vai trong chat, đọc bằng giọng nói ra chữ), viết (đề bài, sửa lỗi, bản viết lại), chữ (kanji, hán tự); theo việc của lộ trình hoặc khi hỏi | Giáo viên, Soạn đề tìm bài đọc và nguồn nghe |
| Đề luyện thi | "Cho mình đề JLPT N3 ngữ pháp", hoặc lịch **Đề luyện thi hằng tuần**; `#mock-test` | Soạn đề (tra web), Chấm bài soát |

## Agent
- **Giáo viên** (mặc định): nói chuyện với người học, giải thích, ghi sổ, điều phối.
- **Soạn đề**: soạn bài ôn và đề thi; được tra web để biết mẫu đề thật.
- **Chấm bài**: chấm, giải thích lỗi, cập nhật lịch ôn; soát ghi chép và đề. Nên gắn kết nối AI khác hãng với Soạn đề.

## Dữ liệu
- `vocabulary`: từ, cách đọc, nghĩa, ví dụ, trình độ, `topics`, `tags`, hộp ôn, ngày ôn, số lần đúng/sai.
- `grammar`: mẫu ngữ pháp, cấu trúc, ví dụ, cùng các trường ôn tập.
- `mistakes`: câu đã viết hoặc nói sai, câu đúng, vì sao, số lần sai; được ôn lại (đặt câu mới cùng lỗi) đến khi sửa được.
- `exercises`: bài ôn và đề thi đã soạn, điểm, phần yếu.
- `schedule`: việc từng ngày của lộ trình: chỉ tiêu, số phút, trạng thái, kết quả.

Cách lập và điều chỉnh lộ trình ở skill `roadmap`; luyện nghe, nói, đọc, viết, chữ ở skill `skills-practice`; quy ước tag ở skill `notes`; lịch ôn ở skill `review`; cách ra đề ở skill `mock-test`.

## Thư mục
- `docs/profile.md`: hồ sơ người học (điền ở buổi đầu), có lộ trình hiện tại.
- `roadmap/`: các lộ trình, mỗi mục tiêu một file, có lịch sử điều chỉnh.
- `lessons/`: mỗi buổi một file bài học, cuối file có nhật ký ngắn (học gì, khó gì, mức tự tin 1-5).
- `exercises/`: bài ôn tập và đáp án.
- `exams/`: đề luyện thi và đáp án.
- `resources/exam-formats.md`: cấu trúc các kỳ thi đã tra, có nguồn.
- `resources/listening-sources.md`: nguồn nghe thật đã tra (link, trình độ, transcript).
- `resources/level-standards.md`: yêu cầu từng trình độ (số từ, chữ, ngữ pháp, điểm đỗ), có nguồn.

## Bắt đầu
1. Mở chat với Giáo viên, nói ngôn ngữ muốn học, trình độ, mục tiêu (kỳ thi, ngày thi), mỗi ngày học bao lâu.
2. Có hạn chót: nhờ lập lộ trình ("đỗ N3 trong 2 tháng"); Giáo viên báo có kịp không và xếp lịch học.
3. Gửi những từ đầu tiên.
4. Bật lịch **Ôn tập hằng ngày**, **Điều chỉnh lộ trình hằng tuần** (khi có lộ trình) và **Đề luyện thi hằng tuần** (nếu đang luyện thi) ở trang Tự động hóa của project; ba lịch tắt sẵn.
