# Bán hàng và tìm khách

Repo bán hàng của một doanh nghiệp nhỏ, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/sales`.

> **Office soạn, bạn gửi.** Agent không gửi tin cho khách, không đăng nhập email/LinkedIn/Zalo của bạn, không mua danh sách hay công cụ. Mọi thư chào đã qua soát; bạn gửi từ tài khoản của mình rồi báo lại để agent theo dõi.

## Luồng
```
chân dung khách (docs/icp.md) + sản phẩm (docs/offer.md)
        │
        ▼
tìm khách có nguồn (#find-leads) → chấm điểm A/B/C (#score-leads)
        │
        ▼
soạn thư chào → soát → bạn gửi (#outreach-draft)
        │
        ▼
nhắc theo dõi, ghi trả lời, mở cơ hội (#follow-up) → bài học tuần
```

## Agent
| Agent | Việc | Quyền |
|---|---|---|
| Trưởng kinh doanh (mặc định) | Nói chuyện với bạn, giữ chân dung khách và sản phẩm, điều phối, báo pipeline | edit |
| Tìm khách | Tìm khách từ nguồn công khai, có link; chấm điểm | edit + web |
| Soạn thư chào | Thư chào và tin theo dõi cá nhân hóa | edit |
| Soát bán hàng | Soát khách và thư: đúng nguồn, đúng luật, không bịa; nên chạy model khác hãng | read + web |

## Quy trình
| Quy trình | Việc |
|---|---|
| `#find-leads` | Tìm khách cho một phân khúc, có nguồn và lý do liên hệ lúc này |
| `#score-leads` | Chấm điểm theo phù hợp, nhu cầu, thời điểm, liên hệ được; hạng A/B/C |
| `#outreach-draft` | Thư chào cho khách hạng A, đã soát, để bạn gửi |
| `#follow-up` | Nhắc lại tối đa 3 lần, ghi trả lời, mở cơ hội, đóng khách từ chối |

## Lịch (tắt sẵn)
- Nhắc theo dõi buổi sáng (8:30 thứ Hai đến thứ Sáu)
- Tìm khách hằng tuần (9:00 thứ Ba)
- Đánh giá pipeline (16:00 thứ Sáu)

## Bảng dữ liệu
`leads` (khách tiềm năng), `deals` (cơ hội), `touches` (mỗi lần liên hệ), `lessons` (bài học).

## Bắt đầu
1. Điền `docs/offer.md` và `docs/icp.md`, hoặc nhắn Trưởng kinh doanh để nó hỏi bạn.
2. Chạy `#find-leads` với một phân khúc, ví dụ "quán cà phê ở Quận 1, 20 khách".
3. `#score-leads`, rồi `#outreach-draft` cho hạng A. Gửi xong, nhắn "đã gửi" để agent ghi lại.

## Công cụ
| Công cụ | Để làm gì |
|---|---|
| Web (có sẵn cho Tìm khách) | Tìm doanh nghiệp, tin tức, tuyển dụng |
| Skill thư viện `maps-leads`, `research-brief` (find_skills) | Tìm khách trên bản đồ, hồ sơ công ty |
| MCP CRM (HubSpot, Pipedrive… nếu bạn có) | Đồng bộ pipeline; chỉ khi bạn kết nối |

Agent không tự cài gì và không dùng dịch vụ trả phí.

## Agent không bao giờ
- Gửi tin, gọi điện, kết bạn thay bạn; đăng nhập tài khoản của bạn.
- Lấy số điện thoại hay email cá nhân, đoán email, cào dữ liệu sau đăng nhập.
- Bịa khách hàng, kết quả, lời chứng thực, giảm giá, hạn chót; gây áp lực.
- Soạn cho người đã từ chối (`do_not_contact`). Luôn có dòng để người nhận từ chối (Nghị định 91/2020 về chống tin rác, Nghị định 13/2023 về dữ liệu cá nhân; GDPR, CAN-SPAM khi gửi ra nước ngoài).
