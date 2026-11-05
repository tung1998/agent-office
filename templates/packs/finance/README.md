# Tài chính cá nhân

Sổ tiền của một người, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/finance`.

> **Office không đụng vào tiền.** Agent không chuyển tiền, không thanh toán, không kết nối ngân hàng hay ví điện tử, không hỏi và không lưu mật khẩu, mã OTP, số thẻ, số tài khoản đầy đủ. Số liệu và lựa chọn để bạn quyết, không phải lời khuyên tài chính, thuế hay pháp lý.

## Cách dùng
- Nhắn như nói chuyện: "cà phê 45k", "lương 25tr", "grab 32k hôm qua". Gửi ảnh hóa đơn hoặc file sao kê (CSV, Excel, PDF) để ghi cả loạt.
- Hỏi: "tháng này ăn uống hết bao nhiêu", "còn bao nhiêu cho mua sắm", "mua laptop 25tr có được không".
- Đầu tháng: báo cáo tháng trước, ngân sách tháng này, tiến độ mục tiêu.

## Agent
| Agent | Việc |
|---|---|
| Quản lý tài chính (mặc định) | Trò chuyện, ghi khoản lẻ, cảnh báo ngân sách, điều phối |
| Ghi sổ | Ghi từng khoản từ tin nhắn, hóa đơn, sao kê; không trùng |
| Phân tích chi tiêu | Báo cáo, ngân sách, mục tiêu; số liệu tính bằng script |
| Soát số liệu | Đối chiếu với nguồn, tính lại tổng (nên chạy model khác hãng) |

## Quy trình
| Quy trình | Khi nào |
|---|---|
| `#record-batch` | Ghi cả loạt: hóa đơn, sao kê, danh sách trong ngày |
| `#monthly-report` | Báo cáo tháng (`reports/<YYYY-MM>.md`) |
| `#budget-plan` | Lập ngân sách tháng tới, bạn duyệt rồi mới ghi |
| `#goal-check` | Tiến độ mục tiêu tiết kiệm và trả nợ |

## Lịch (tắt sẵn, bật trong Tự động hóa)
- Nhắc ghi chi tiêu 21:00 mỗi ngày
- Kiểm tra ngân sách 20:00 Chủ nhật
- Báo cáo tháng 9:00 ngày 1

## Dữ liệu
Bảng `transactions` (giao dịch), `budgets` (ngân sách), `goals` (mục tiêu và nợ), `accounts` (tài khoản theo tên gọi), `lessons` (thói quen rút ra). File: `docs/profile.md`, `docs/categories.md`, `reports/`, `receipts/`.

## Công cụ
- Python 3 (có sẵn trên macOS và Linux) để chạy `.claude/skills/report/totals.py`; thiếu thì agent cộng bằng truy vấn và nói rõ.
- Không cần MCP nào. Không kết nối ngân hàng: xuất sao kê từ app ngân hàng rồi gửi file.

## Bắt đầu
Điền `docs/profile.md` (hoặc trả lời câu hỏi của Quản lý tài chính ở buổi đầu), xem lại `docs/categories.md`, rồi nhắn khoản chi đầu tiên.
