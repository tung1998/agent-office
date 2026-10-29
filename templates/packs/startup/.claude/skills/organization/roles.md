# Lập đội: vai mẫu để tạo agent

Mọi agent của công ty lấy từ thư viện agent của office. Ban giám đốc tạo người của Ban giám đốc; mỗi dự án (đơn vị) tự lập đội của nó, trưởng dự án tạo. Tạo agent khi điều kiện của vai xảy ra, không tạo trước. Cách tạo: `get` resource `agent_library` id ở bảng dưới, rồi `propose_change` resource `agent` op `create` từ mẫu đó (`key`, `name`, `role`, `description`, `model_tier`, `permissions`, `instructions`), chỉ sửa cho khớp việc thật của công ty. Agent được tạo ngay (tối đa 15 agent một project). Trưởng nhóm (mức Vận hành có `self.manage`: trưởng đơn vị, quản lý mảng, đồng CEO) do một lượt administrator tạo hay nâng lên thì office tự cho chạy administrator như CEO, nhân danh cùng người; người làm khác giữ quyền của mẫu. Sau đó ghi `staff`, `org`.

| Vai | Mẫu thư viện | Khi nào tạo |
|---|---|---|
| Thực thi (`implementer`) | `office/implementer` | Có việc đầu tiên cần làm (code, tài liệu, kế hoạch, nội dung) mà chưa có ai làm; thường ngay lượt Chủ tịch giao việc đầu tiên. Thêm người cùng vai (`implementer-2`…) khi có việc song song không đụng cùng file. |
| Kiểm soát (`inspector`) | `office/inspector` | Lần đầu có việc quan trọng cần soát độc lập (#verify) hoặc quyết định lớn cần phản biện (#deliberate). Nhắc Chủ tịch chọn kết nối AI khác hãng với người làm (`provider_id`). |
| Nghiên cứu (`researcher`) | `office/researcher` | Lần đầu cần thông tin bên ngoài: câu trả lời có "Cần nghiên cứu: …", hoặc kế hoạch cần số liệu thị trường, đối thủ, công cụ. Quyền đọc web tách khỏi người có quyền cao. |
| Dùng thử (`trial-user`) | `office/trial-user` | Có bản chạy được đầu tiên của một sản phẩm: dùng nó như người dùng thật ở đúng chỗ họ dùng; một bản chỉ xong khi Dùng thử kết luận DÙNG ĐƯỢC. Không sửa code. |

Vai khác (quản lý mảng, chuyên môn riêng): tìm trong `agent_library` trước (`list` resource `agent_library`), chỉ viết từ đầu khi không có mẫu hợp. Trưởng đơn vị đi cùng mẫu đơn vị (`office/unit-head` kèm mục mảng của đơn vị).

## Đội theo loại dự án

Lập đội như một công ty thật sẽ thuê cho loại việc đó; thiếu vai chuyên môn thì kết quả sẽ thiếu đúng phần ấy. Mẫu trong thư viện (`get` resource `agent_library`); mẫu `agency/…` chỉ đọc, ai cần sửa code thì nâng quyền khi tạo.

| Loại dự án | Vai cần có | Mẫu thư viện |
|---|---|---|
| Sản phẩm có giao diện (web, app, extension) | Dùng thử, thiết kế UI, nghiên cứu UX, frontend, kiểm thử | `office/trial-user`, `agency/ui-designer`, `agency/ux-researcher` (hay `agency/ux-architect`), `office/frontend-dev`, `office/qa-tester` |
| Backend, API, dữ liệu | Backend, kiểm thử, bảo mật, vận hành | `office/backend-dev`, `office/qa-tester`, `office/security-reviewer`, `office/devops` |
| App di động | Dùng thử, thiết kế UI, app mobile, kiểm thử, phát hành | `office/trial-user`, `agency/ui-designer`, `agency/mobile-app-builder`, `office/qa-tester`, `agency/mobile-release-engineer` |
| Nội dung, marketing | Viết nội dung, SEO, mạng xã hội, thương hiệu | `office/content-writer`, `office/seo-specialist`, `office/social-media`, `agency/brand-guardian` |
| Số liệu, phân tích | Phân tích, kỹ sư dữ liệu | `office/data-analyst`, `office/data-engineer` |
| Chăm sóc khách | Hỗ trợ khách, phân tích phản hồi | `office/support-agent`, `office/feedback-analyst` |

Đội nào cũng cần người soát độc lập (`office/inspector`, model khác hãng) cho việc quan trọng. Bắt đầu với vai tối thiểu để ra bản đầu tiên, thêm người khi `staff_load` cho thấy dồn việc.
