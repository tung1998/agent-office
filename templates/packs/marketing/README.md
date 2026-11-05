# Marketing

Repo marketing của một doanh nghiệp nhỏ, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/marketing`.

> **Office không tự đăng, không tự chi tiền.** Agent không đăng bài, không gửi email hay tin nhắn, không bật quảng cáo, không đăng nhập tài khoản của bạn. Agent làm tới bản đã soát kèm checklist đăng; bạn duyệt, tự đăng rồi gửi lại link và số liệu.

## Luồng

```
mục tiêu ─► kế hoạch chiến dịch (#campaign-plan) ─► bạn duyệt kế hoạch, ngân sách
                     │
                     ▼
        nội dung theo kênh (#make-assets) ─► cổng soát ─► bạn duyệt, tự đăng, gửi link
                     │
                     ▼
     số liệu bạn xuất ─► đánh giá (#campaign-review) ─► bài học ─► thử A/B (#ab-test)
```

## Agent

| Agent | Việc |
|---|---|
| Trưởng marketing (mặc định) | Nói chuyện với bạn, giữ `docs/brand.md`, điều phối quy trình |
| Chiến lược marketing | Nghiên cứu khách hàng, đối thủ, kênh có nguồn; kế hoạch; thiết kế và đọc thử nghiệm |
| Sáng tạo nội dung | Bài đăng, quảng cáo, email, trang đích, brief ảnh/video, có biến thể |
| Soát nội dung marketing | Cổng soát: sự thật, chính sách nền tảng, bản quyền, giọng thương hiệu (nên chạy model khác hãng) |

## Quy trình

| Quy trình | Làm gì |
|---|---|
| `#campaign-plan` | Mục tiêu → kế hoạch có chỉ số đo, kênh, lịch, ngân sách đề xuất; có phản biện |
| `#make-assets` | Làm nội dung theo kế hoạch, qua cổng soát, chờ bạn đăng |
| `#ab-test` | Thiết kế hoặc đọc thử nghiệm A/B theo bậc (hook → hình → ưu đãi → đối tượng → kênh → trang đích) |
| `#campaign-review` | Đánh giá từ số liệu bạn xuất, rút bài học |

## Bảng dữ liệu
`campaigns` (chiến dịch), `assets` (từng nội dung, link khi đã đăng, số liệu), `experiments` (thử nghiệm A/B), `lessons` (bài học).

## Lịch (tắt sẵn, bật trong Tự động hóa)
- **Kế hoạch nội dung tuần**: thứ Hai 9:00.
- **Đánh giá chiến dịch hằng tuần**: thứ Sáu 16:00, nhắc bạn gửi số liệu.

## Công cụ nên kết nối (không bắt buộc)
| Công cụ | Để làm gì |
|---|---|
| Skill thư viện office: marketingskills, content-repurpose, homepage-copy, research-brief | Agent tìm bằng `find_skills` khi cần |
| MCP Google Analytics, Meta Ads, công cụ email (chỉ đọc) | Lấy số liệu thay vì bạn dán; kết nối trong cài đặt MCP của office |
| Canva / công cụ thiết kế | Bạn hoặc agent dựng ảnh từ brief |

Không kết nối gì vẫn chạy được: bạn dán bảng hoặc gửi file CSV xuất từ các công cụ.

## Bắt đầu
1. Nói với Trưởng marketing doanh nghiệp của bạn để điền `docs/brand.md`.
2. Nêu một mục tiêu ("thêm 50 khách đặt lịch trong tháng 12"), agent chạy `#campaign-plan`.

## Agent không bao giờ
Đăng, gửi, bật hay chi tiền quảng cáo; đăng nhập tài khoản của bạn; viết đánh giá, lời chứng thực hay số liệu bịa.
