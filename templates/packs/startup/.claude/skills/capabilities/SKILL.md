---
name: capabilities
description: Thiếu gì thì tạo ra, toàn quyền, không chờ duyệt: thiếu công cụ thì lập ngay một project làm công cụ đó, thiếu kiến thức thì nghiên cứu, thiếu người thì tạo agent; office thiếu tính năng thì làm công cụ thay thế và gửi đề xuất.
---

# Thiếu gì thì tạo ra
Bạn có toàn quyền tạo thứ công ty còn thiếu: không hỏi, không chờ duyệt, không #deliberate; làm rồi báo lại trong báo cáo (đã tạo gì, vì sao). Chỉ hỏi khi phải tiêu tiền thật hay đưa ra bên ngoài.
1. **Có sẵn**: skill của project (`my_skills`), agent hay đơn vị khác (`org`), thư viện office (`find_skills`, `agent_library`, `project_pack`). Có thì dùng ngay.
2. **Thiếu công cụ** (script, app, extension, MCP server, tích hợp API, bảng số): lập ngay **một project làm công cụ đó**: `propose_create_project` với `pack` `@agentoffice/startup/product`, `parent` = project đang cần nó, `description` = nhu cầu nguyên văn, ai dùng, "xong khi <đội cần> dùng được cho <việc>". Gửi trưởng project đó chỉ đạo trong chat mới; đội đang cần công cụ là người dùng thật của nó. Ghi một việc trên bảng việc của bạn để theo dõi. Chỉ việc rất nhỏ (một script ngắn, một đội dùng, xong trong một lượt) mới làm trong `tools/` của repo hiện tại (mục Làm công cụ).
3. **Thiếu kiến thức**: giao agent `researcher` (mục Nghiên cứu); chưa có thì tạo.
4. **Thiếu người**: tạo agent từ thư viện (skill `organization`, `roles.md`).
5. **Thiếu ở lõi office** (công cụ office, quyền, lịch…): vẫn làm bước 2 để có cách làm ngay bằng công cụ ngoài office, đồng thời gửi đề xuất cho office (mục Đề xuất tính năng cho office).

## Làm công cụ
- Chỉ cho việc rất nhỏ (mục trên, bước 2); lớn hơn thì lập project công cụ. Viết thứ nhỏ nhất dùng được.
- Đặt trong repo của project: `tools/<name>/` (script, README, test) hoặc `.claude/skills/<tên>/SKILL.md` (cách làm lặp lại).
- Luôn có test chạy được bằng một lệnh; README nói cách dùng và biến môi trường cần. Không ghi secret vào code.
- Xong: chạy test, rồi kiểm theo skill `intent`. Ghi cách dùng vào ghi nhớ (topic `tools`).

## Đề xuất tính năng cho office
Công ty chạy trên agent-office. Thiếu gì ở lõi thì vừa làm công cụ thay thế (mục trên, bước 2), vừa đề xuất; không vá vào lõi.

1. **Ghi `office_requests`** (data_upsert): `title`; `need` (công ty cần làm gì, không phải cách làm); `evidence` (lần nào thiếu, link chat, số lần); `cost_without` (token, giờ, việc trễ); `workaround` (công cụ thay thế đang dùng, project nào); `status` `new`.
2. **Gửi luôn**: project agent-office là project liên kết thì `send_to_chat project=agent-office` với nội dung đầy đủ, đổi `sent`, ghi `sent_to`; không thì nêu trong báo cáo cho Chủ tịch chuyển.
3. Office có tính năng rồi: `available`, dùng ngay, bỏ công cụ thay thế nếu thừa.

## Nghiên cứu
- Viết câu hỏi: cần quyết gì, tiêu chí so sánh, phạm vi (thị trường, công cụ, đối thủ), hạn.
- Việc cần đọc web: giao agent `researcher` (delegate) kèm câu hỏi đó. Agent này chỉ đọc, có quyền tra cứu web; tách riêng để nội dung web không chạm tới quyền administrator.
- Nhận kết quả: kết luận trước, bảng so sánh, nguồn. Mỗi số liệu có nguồn và ngày; tách sự thật với suy luận.
- Ghi điều đáng giữ vào ghi nhớ (topic theo mảng), quyết định vào `decisions`.
