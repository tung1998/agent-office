# Chăm sóc khách hàng

Repo chăm sóc khách hàng của một shop hoặc dịch vụ, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/support`. Mẫu bật sẵn **gói Chăm sóc khách hàng** của office: ticket, Hộp thư, loại case, SLA, kho câu trả lời (phương án), bot Discord/Telegram, form web, Crisp, email.

> **Agent không tự hứa và không tự gửi khi bạn chưa cho phép.** Nguồn mới để chế độ `approve`: mỗi câu trả lời chờ bạn duyệt trong Hộp thư. Hoàn tiền, đền bù, ngày giao, giá, tính năng chưa có: luôn chuyển bạn quyết. Khách đòi gặp người: AI dừng hẳn ở ticket đó.

## Agent
| Agent | Việc | Quyền |
|---|---|---|
| Trưởng CSKH (mặc định) | Nói chuyện với bạn, giữ chính sách và kho câu trả lời, phân loại, báo cáo | edit |
| Trả lời khách | Trả lời ticket từ kho câu trả lời và chính sách; là agent bạn chọn cho các nguồn | propose |
| Soát câu trả lời | Soát bản nháp và playbook, chấm chất lượng hằng tuần; nên chạy model khác hãng | read |

## Quy trình
| Quy trình | Khi nào |
|---|---|
| `#triage-tickets` | Mỗi sáng: đặt loại case, ticket kẹt/quá SLA, việc bạn cần làm |
| `#answer-ticket` | Ticket khó: bản nháp có nguồn, đã soát, để bạn gửi từ Hộp thư |
| `#escalate` | Hoàn tiền, đền bù, khiếu nại pháp lý, yêu cầu dữ liệu: tóm tắt, phương án, đề xuất để bạn quyết |
| `#weekly-faq` | Hằng tuần: câu hỏi lặp lại và câu AI chưa trả lời được thành playbook trong `docs/kb/` |
| `#quality-review` | Hằng tuần: chấm 20 câu trả lời, sửa kho câu trả lời, báo loại case nào đủ tốt để tự gửi |

## Lịch (tắt sẵn, bật trong Tự động hóa)
- Phân loại ticket buổi sáng (8:00 hằng ngày)
- Cập nhật kho câu trả lời (9:00 thứ Hai)
- Chấm chất lượng (10:00 thứ Hai)

## Bảng dữ liệu
`feedback` (lỗi, mong muốn khách báo, có số lần gặp), `escalations` (ca chờ bạn quyết), `reviews` (kết quả chấm tuần). Khách hàng và phương án là bảng `customers`, `playbooks` của gói Chăm sóc khách hàng.

## Bắt đầu
1. Điền `docs/policies.md` (bán gì, đổi trả, giao hàng, bảo hành, giờ làm việc) và `docs/voice.md` (xưng hô, giọng). Hoặc nhắn Trưởng CSKH để nó hỏi bạn.
2. Khách hàng → Nguồn: thêm nguồn (bot Discord/Telegram của project, form web, Crisp, email), chọn agent **Trả lời khách**, chế độ `approve`.
3. Khách hàng → Phương án: thêm các file trong `docs/kb/` (loại file) để agent tìm được.
4. Khách hàng → Cài đặt: loại case (đơn hàng, đổi trả, kỹ thuật…), SLA, từ khách đòi gặp người.
5. Bật các lịch khi đã có ticket.

## Công cụ nên kết nối
| Công cụ | Để làm gì |
|---|---|
| Bot Discord/Telegram của office | Nhận tin khách qua chat |
| Crisp (nguồn của gói) | Chat trên website |
| SMTP (nguồn email) | Trả lời qua email |
| MCP của hệ thống bán hàng (nếu có: Shopify, Haravan…) | Tra đơn hàng, vận chuyển thay vì đoán; chỉ đọc |

Agent không tự cài gì; thiếu công cụ thì nhờ bạn kết nối trong cài đặt MCP của office.

## Agent không bao giờ
- Gửi cho khách khi nguồn đang ở chế độ duyệt mà bạn chưa duyệt; nhắn khách trước (chỉ trả lời khi khách nhắn).
- Hứa hoàn tiền, đền bù, ngày, giá, tính năng ngoài chính sách.
- Dùng dữ liệu khách cho việc khác, gửi ra ngoài; hỏi mật khẩu, số thẻ.
- Viết đánh giá giả, xin đánh giá đổi quà.
