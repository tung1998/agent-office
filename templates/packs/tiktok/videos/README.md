# Video

Mỗi video một thư mục `videos/<YYYY-MM-DD>-<slug>/`:

| File | Là gì | Ai làm |
|---|---|---|
| `script.md` | Kịch bản theo giây: hook, lời, chữ trên màn hình, cảnh, caption, hashtag, nguồn | Biên kịch |
| `shotlist.md` | Danh sách cảnh, tư liệu dùng và giấy phép; không có clip thì là hướng dẫn quay cho bạn | Dựng video |
| `raw/` | Clip bạn quay (bỏ vào đây; agent không sửa file gốc) | Bạn |
| `out/` | Bản dựng tạm | Dựng video |
| `captions.srt` | Phụ đề (whisper, đã soát tên riêng) | Dựng video |
| `cover.jpg` | Ảnh bìa | Dựng video |
| `final.mp4` | Video 1080x1920 sẵn đăng | Dựng video |
| `post.md` | Caption, hashtag, nhạc nên chọn, nhãn AI / hợp tác trả phí, giờ đăng | Quản lý kênh |

Video nặng: cân nhắc thêm `videos/**/raw/` và `videos/**/out/` vào `.gitignore` để repo không phình.
