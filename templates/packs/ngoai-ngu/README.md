# Học ngoại ngữ

Repo học ngoại ngữ của một người, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/ngoai-ngu`. Repo giữ sổ học (bài học, bài tập, đề thi) dạng Markdown; từ vựng và ngữ pháp nằm trong bảng dữ liệu của project để lọc theo tag và lịch ôn.

## Luồng học

```
thêm từ mới ──► sổ: tu_vung, ngu_phap (có tag) + bai-hoc/<ngày>.md
                    │
                    ▼
ôn tập (hộp Leitner, next_review) ──► chấm ──► lên/xuống hộp, tag loi:*
                    │
                    ▼
đề theo mẫu kỳ thi (đã học + mẫu đề thật trên mạng) ──► chấm ──► điểm, phần yếu
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Thêm từ mới | Gửi từ, câu, đoạn văn, ảnh sách, link vào chat; nhiều mục thì Giáo viên chạy `#them-tu` | Giáo viên ghi, Chấm bài soát |
| Ôn tập | "Ôn bài" trong chat, hoặc lịch **Ôn tập hằng ngày**; `#on-tap` | Soạn đề soạn, Giáo viên hỏi |
| Chấm | Trả lời trong chat; `#cham-bai` | Chấm bài |
| Đề luyện thi | "Cho mình đề JLPT N3 ngữ pháp", hoặc lịch **Đề luyện thi hằng tuần**; `#ra-de` | Soạn đề (tra web), Chấm bài soát |

## Agent
- **Giáo viên** (mặc định): nói chuyện với người học, giải thích, ghi sổ, điều phối.
- **Soạn đề**: soạn bài ôn và đề thi; được tra web để biết mẫu đề thật.
- **Chấm bài**: chấm, giải thích lỗi, cập nhật lịch ôn; soát ghi chép và đề. Nên gắn kết nối AI khác hãng với Soạn đề.

## Dữ liệu
- `tu_vung`: từ, cách đọc, nghĩa, ví dụ, trình độ, `topics`, `tags`, hộp ôn, ngày ôn, số lần đúng/sai.
- `ngu_phap`: mẫu ngữ pháp, cấu trúc, ví dụ, cùng các trường ôn tập.
- `bai_tap`: bài ôn và đề thi đã soạn, điểm, phần yếu.

Quy ước tag ở skill `ghi-bai`; lịch ôn ở skill `on-tap`; cách ra đề ở skill `ra-de`.

## Thư mục
- `docs/ho-so.md`: hồ sơ người học (điền ở buổi đầu).
- `bai-hoc/`: mỗi buổi một file bài học.
- `bai-tap/`: bài ôn tập và đáp án.
- `de-thi/`: đề luyện thi và đáp án.
- `tai-lieu/mau-de.md`: cấu trúc các kỳ thi đã tra, có nguồn.

## Bắt đầu
1. Mở chat với Giáo viên, nói ngôn ngữ muốn học, trình độ, mục tiêu (kỳ thi, ngày thi), mỗi ngày học bao lâu.
2. Gửi những từ đầu tiên.
3. Bật lịch **Ôn tập hằng ngày** (và **Đề luyện thi hằng tuần** nếu đang luyện thi) ở trang Tự động hóa của project; hai lịch tắt sẵn.
