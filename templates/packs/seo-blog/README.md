# Blog và SEO

Repo blog và SEO của một website, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/seo-blog`.

> **Office không tự đăng.** Agent không đăng bài lên CMS, không đăng nhập tài khoản của bạn, không mua công cụ. Agent làm tới bài đã soát; bạn đăng rồi gửi lại URL.

## Luồng

```
nghiên cứu từ khóa (#keyword-research) ─► brief ─► bài viết (#write-article) ─► biên tập soát
                                                                        │
                                                         bạn đăng, gửi URL
                                                                        ▼
       số liệu Search Console bạn xuất ─► soát hằng tháng (#monthly-audit) ─► làm mới bài (#refresh-post)
```

## Agent

| Agent | Việc |
|---|---|
| Trưởng biên tập SEO (mặc định) | Nói chuyện với bạn, giữ `docs/site.md` và lịch bài, điều phối |
| Nghiên cứu từ khóa | Từ khóa, ý định, trang kết quả, khoảng trống, cụm chủ đề, có nguồn |
| Viết bài SEO | Brief, bài viết, làm mới bài cũ, chuẩn on-page |
| Biên tập SEO | Soát trước khi bạn duyệt: ý định, sự thật, sao chép, on-page (nên chạy model khác hãng) |

## Quy trình

| Quy trình | Làm gì |
|---|---|
| `#keyword-research` | Một chủ đề → cụm từ khóa có ý định và ưu tiên |
| `#write-article` | Một từ khóa → brief → bài → soát → chờ bạn đăng |
| `#refresh-post` | Làm mới bài tụt hạng hoặc cũ, giữ URL |
| `#monthly-audit` | Đọc file Search Console bạn xuất, tìm bài cần làm mới và chủ đề mới |

## Bảng dữ liệu
`keywords` (từ khóa), `articles` (bài viết, URL, số liệu), `lessons` (bài học).

## Lịch (tắt sẵn)
- **Lịch bài tuần**: thứ Hai 9:00.
- **Nhắc soát SEO hằng tháng**: ngày 2 mỗi tháng, nhắc bạn xuất Search Console.

## Công cụ
| Công cụ | Để làm gì |
|---|---|
| Google Search Console (bạn xuất CSV, hoặc kết nối MCP chỉ đọc) | Số liệu thật: lượt bấm, hiển thị, vị trí |
| Skill thư viện office: marketingskills (SEO, AEO), research-brief | Agent tìm bằng `find_skills` |
| Công cụ SEO trả phí (Ahrefs, Semrush) | Chỉ khi bạn kết nối tài khoản của mình; không bắt buộc |

## Agent không bao giờ
Đăng bài, mua công cụ, bịa lượt tìm kiếm, đánh giá, trải nghiệm hay số liệu; nhồi từ khóa, sao chép bài người khác, mua liên kết.
