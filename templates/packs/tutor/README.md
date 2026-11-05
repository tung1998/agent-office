# Gia sư

Gia sư một kèm một cho bất kỳ môn nào, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/tutor`. Ý tưởng từ các lớp học AI như OpenMAIC, DeepTutor: tài liệu thành bài giảng, luyện tập, phản hồi; cộng thêm sổ khái niệm ôn theo hộp Leitner như mẫu học ngoại ngữ.

> **Học, không làm hộ.** Bài về nhà có chấm điểm, bài thi mang về của trường được giải thích bằng gợi ý và ví dụ tương tự, không đưa lời giải để nộp.

## Cách dùng
- Gửi tài liệu (chương sách, slide, link, ảnh ghi chép) hoặc nêu chủ đề → bài học theo trình độ.
- Hỏi bất cứ chỗ nào chưa hiểu → giải thích từng bước, gợi ý dần khi bạn bí.
- Mỗi ngày ôn các khái niệm đến hạn; cuối tuần bài luyện; có mục tiêu theo ngày thì có lộ trình và đề thi thử.

## Agent
| Agent | Việc |
|---|---|
| Gia sư (mặc định) | Trò chuyện, soạn bài, giảng, lộ trình, điều phối |
| Soạn bài tập | Bài luyện, đề kiểm tra, đề thi thử theo mẫu thật, đáp án và lời giải riêng |
| Chấm bài | Chấm theo đáp án, chạy code bài lập trình, cập nhật hộp ôn và lỗi sai |
| Soát nội dung | Kiểm kiến thức, đáp án, độ khó trước khi tới tay bạn (nên chạy model khác hãng) |

## Quy trình
| Quy trình | Khi nào |
|---|---|
| `#make-lesson` | Có tài liệu hay chủ đề mới |
| `#practice` | Luyện các khái niệm đến hạn và chỗ yếu |
| `#grade` | Nộp bài làm |
| `#mock-test` | Đề thi thử hay kiểm tra cuối chủ đề |

## Lịch (tắt sẵn, bật trong Tự động hóa)
- Học hằng ngày 20:00
- Điều chỉnh lộ trình 19:00 thứ Bảy
- Bài luyện cuối tuần 9:00 Chủ nhật

## Dữ liệu
Bảng `concepts` (khái niệm, hộp ôn), `exercises` (bài tập, điểm), `mistakes` (lỗi sai), `schedule` (lịch học). File: `docs/profile.md`, `materials/`, `lessons/`, `exercises/`, `roadmap/`, `resources/exam-formats.md`.

## Công cụ
- Python 3 để chạy `.claude/skills/roadmap/leitner.py` (số liệu ôn tập) và chấm bài lập trình Python; môn lập trình khác cần trình biên dịch tương ứng trên máy (Node, Go, Java…), thiếu thì Chấm bài nói rõ.
- Soạn bài tập cần quyền tra web để kiểm cấu trúc kỳ thi.
- Tùy chọn: MCP context7 để tra tài liệu thư viện lập trình; skill thư viện tìm bằng `find_skills` (vd vẽ sơ đồ, làm slide).

## Bắt đầu
Trả lời vài câu hỏi đầu của Gia sư (hoặc điền `docs/profile.md`), rồi gửi tài liệu đầu tiên.
