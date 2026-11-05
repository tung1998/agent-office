# Kênh TikTok

Repo làm video TikTok của một nhà sáng tạo, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/tiktok`.

> **Office không tự đăng.** Agent không đăng nhập TikTok, không đăng, hẹn giờ hay xóa video, không giữ mật khẩu của bạn, không mua gì. Agent làm tới file sẵn đăng kèm caption và danh sách việc cần làm khi đăng; bạn đăng rồi gửi lại link.

## Luồng

```
hồ sơ kênh (docs/channel.md)
        │
        ▼
ý tưởng theo trend có nguồn, có điểm (ideas, trends) ──► bạn chọn
        │
        ▼
kịch bản theo giây ──► soát ──► dựng 9:16, phụ đề, ảnh bìa ──► soát ──► post.md
        │                                                            │
        │                                                bạn đăng, gửi link
        ▼                                                            ▼
số liệu tuần bạn dán vào ──► đánh giá ──► bài học (lessons) ──► ý tưởng lần sau
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Ý tưởng | "Cho mình 10 ý tưởng tuần này"; `#video-ideas` | Biên kịch viết, Soát nội dung lọc |
| Làm video | "Làm video từ ý tưởng số 3, clip ở videos/…/raw"; `#make-video` | Biên kịch → Soát → Dựng video → Soát |
| Cắt video dài | "Cắt podcast này thành 3 video ngắn"; `#repurpose` | Dựng video chép lời, Biên kịch chọn đoạn, Soát kiểm |
| Duyệt đăng | Xem `final.mp4`, nói "ok đăng" | Chỉ khi Soát nội dung PASS **và** bạn đồng ý, video mới thành `approved` |
| Thử A/B | "Thử hook kiểu câu hỏi với kiểu kết quả trước"; `#ab-test` | Biên kịch viết hai phương án, Soát nội dung soát cách thử và kết luận |
| Đánh giá | Dán số liệu TikTok Studio; `#weekly-review` | Quản lý kênh đánh giá, Soát nội dung soát bài học |

## Agent
- **Quản lý kênh** (mặc định): nói chuyện với bạn, giữ hồ sơ kênh và lịch nội dung, điều phối, ghi số liệu.
- **Biên kịch**: tra trend trên mạng có nguồn, viết ý tưởng, hook, kịch bản, caption, hashtag.
- **Dựng video**: chạy ffmpeg, whisper trên máy để cắt ghép, phụ đề, chuẩn âm lượng, ảnh bìa (quyền Vận hành).
- **Soát nội dung**: kiểm quy định TikTok, bản quyền, nhãn AI, sự thật, sức hút của hook. Nên gắn kết nối AI khác hãng với Biên kịch.

## Dữ liệu
- `ideas`: ý tưởng, hook, định dạng, nguồn trend, điểm.
- `videos`: mỗi video từ nháp tới đã đăng, kết quả soát và người duyệt (cửa duyệt đăng), link, lượt xem, thời gian xem, tỷ lệ xem hết, chia sẻ, lưu, follow.
- `lessons`: bài học từ số liệu; gặp 3 lần thành quy tắc (bạn duyệt).
- `trends`: trend, âm thanh, định dạng đang lên, có nguồn và ngày.
- `experiments`: thang thử A/B qua các lần đăng (hook → ảnh bìa → caption → độ dài → giờ đăng), mỗi lần một yếu tố, kết luận có số.

Cách viết ở skill `hooks-scripts`; dựng ở `production`; soát ở `guidelines`; số liệu và bài học ở `analytics`.

## Công cụ cần cài trên máy
Agent kiểm bằng `which` và báo bạn cài, không tự cài.

| Công cụ | Để làm gì | Cài |
|---|---|---|
| ffmpeg, ffprobe | Cắt ghép, đổi khung 9:16, phụ đề, âm lượng, ảnh bìa | `brew install ffmpeg` |
| whisper hoặc whisper.cpp | Phụ đề tự động | `pip install openai-whisper` hoặc `brew install whisper-cpp` (kèm file model) |

Skill trong thư viện office (Thư viện → Skill, hoặc nhờ agent `find_skills`): `hyperframes` (dựng video từ HTML, chữ chạy, đồ họa), `ad-video-pipeline` (video kiểu quảng cáo), `content-repurpose` (một nội dung thành nhiều bài). Giọng đọc AI (vd MCP ElevenLabs) chỉ dùng khi bạn tự kết nối và đồng ý chi phí.

## Thư mục
- `docs/channel.md`: hồ sơ kênh (ngách, người xem, giọng, trụ cột, nhịp đăng, mục tiêu).
- `docs/calendar.md`: lịch nội dung theo tuần.
- `videos/`: mỗi video một thư mục (xem `videos/README.md`).
- `assets/`: tư liệu dùng chung, mỗi file một dòng giấy phép.
- `reports/`: đánh giá tuần; `data/`: số liệu xuất từ TikTok Studio.

## Bắt đầu
1. Cài ffmpeg (và whisper nếu muốn phụ đề tự động).
2. Mở chat với Quản lý kênh, kể về kênh: ngách, người xem, giọng, bạn có thể quay gì. Nó điền `docs/channel.md`.
3. Xin ý tưởng, chọn một, gửi clip đã quay (hoặc nhờ viết hướng dẫn quay), rồi `#make-video`.
4. Bật lịch **Quét trend hằng ngày**, **Kế hoạch nội dung tuần**, **Đánh giá tuần** ở trang Tự động hóa của project; ba lịch tắt sẵn.
5. Mỗi tuần dán số liệu TikTok Studio để có bài học cho tuần sau.
