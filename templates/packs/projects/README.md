# Quản lý dự án

Repo quản lý dự án và cuộc họp của một nhóm nhỏ, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/projects`.

## Luồng

```
mục tiêu → kế hoạch (plans/): mốc theo kết quả, việc có người làm và hạn → soát kế hoạch → bạn duyệt
ghi chép họp → biên bản (meetings/): quyết định (decisions) + việc (tasks) → tin nhắn tóm tắt để bạn gửi
mỗi ngày: nhắc việc theo người · mỗi tuần: báo cáo tình hình (reports/) · hai tuần: soát rủi ro (risks)
```

## Agent

| Agent | Việc | Quyền |
|---|---|---|
| Quản lý dự án (mặc định) | Kế hoạch, điều phối, cập nhật nhanh | Sửa file |
| Thư ký họp | Biên bản, quyết định, việc cần làm | Sửa file |
| Theo dõi tiến độ | Nhắc hạn, dự báo mốc, báo cáo tuần | Sửa file |
| Soát rủi ro | Phản biện kế hoạch, sổ rủi ro, PASS/FAIL | Chỉ đọc |

Nên cho Soát rủi ro chạy model khác hãng với Quản lý dự án.

## Quy trình

- `#plan-project`: lập kế hoạch từ một mục tiêu, có phản biện.
- `#meeting-notes`: biên bản họp từ ghi chép hoặc bản chép lời.
- `#weekly-status`: báo cáo tình hình tuần.
- `#risk-review`: soát sổ rủi ro.

## Lịch (tắt sẵn)

- **Nhắc việc hằng ngày**: 8:30 thứ Hai đến thứ Sáu.
- **Báo cáo tình hình tuần**: 16:00 thứ Sáu.
- **Soát rủi ro**: ngày 1 và 15 hằng tháng.

## Bảng dữ liệu

`projects` (dự án), `tasks` (việc), `meetings` (cuộc họp), `decisions` (quyết định), `risks` (rủi ro).

## Công cụ nên kết nối

| Công cụ | Để làm gì |
|---|---|
| Skill thư viện `meeting-actions` (tìm bằng `find_skills`) | Rút việc cần làm từ bản chép lời dài |
| MCP lịch, email, chat nhóm (nếu có) | Đọc lịch họp; gửi tin chỉ khi bạn đồng ý từng tin |
| Bot Discord/Telegram của office | Nhận ghi chép họp, gửi nhắc việc bạn đã duyệt |

Thiếu công cụ thì agent nhờ bạn kết nối trong phần MCP của office, không tự cài.

## Giới hạn

- Không gửi gì ra ngoài office (email, chat, lời mời họp) khi bạn chưa đồng ý từng tin: agent soạn sẵn để bạn gửi.
- Không bịa người làm hay hạn cho việc: chưa rõ thì ghi "cần xác nhận".
- Không chi tiền, không đăng ký dịch vụ.

## Bắt đầu

Điền `docs/team.md` (hoặc trả lời Quản lý dự án ở buổi đầu), rồi đưa mục tiêu đầu tiên hoặc dán ghi chép cuộc họp gần nhất.
