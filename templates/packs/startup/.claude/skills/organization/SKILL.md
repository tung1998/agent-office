---
name: organization
description: Đội và tổ chức: khi nào tách agent hay đơn vị, cách lập, gộp lại, đổi vai, nhịp theo cấp; nhân sự khi lớn lên (staff_load, chia việc quản lý, đồng CEO, bàn giao).
---

# Tổ chức: gộp mặc định, tách khi có bằng chứng
Một agent, một bộ nhớ, nhiều skill là mặc định: thấy kết quả các bước trước, mở dần từng lớp thay vì nạp hết. Chỉ tách khi có bằng chứng.

## Tiêu chí tách một agent
- Bộ nhớ quá tải: ghi nhớ bị rút gọn liên tục, nhầm bối cảnh giữa các mảng.
- Cần chạy song song nhiều việc dài.
- Chọn sai công cụ, hoặc chỉ dẫn của các mảng mâu thuẫn nhau.
- Cần tách quyền (đọc web, nội dung bên ngoài; hoặc quyền cao chỉ cho một việc).
- Cần góc nhìn độc lập của model khác hãng (kiểm soát, phản biện).

## Quản lý và nhân sự
- Chia việc quản lý, thêm quản lý mảng, đồng CEO, bàn giao, gộp lại: mục Nhân sự bên dưới, đọc tải bằng `staff_load`.

## Tiêu chí lập đơn vị (project)
- Có repo hay sản phẩm riêng, hoặc một luồng việc song song kéo dài nhiều tuần cần đội riêng.
- **Sản phẩm cấp trên giao** (app, web, extension, công cụ có code riêng): lập đơn vị `product` ngay khi nhận, không cần bằng chứng hay #deliberate; code sản phẩm không nằm trong repo của cấp trên.
- **Công cụ công ty còn thiếu** (skill `capabilities`, bước 2): lập project `product` làm công cụ đó ngay, cũng không cần bằng chứng hay #deliberate.
- Không lập đơn vị theo chức năng (marketing, kế toán…) khi một agent với skill làm được.

## Cách làm
1. Ghi `decisions` (vì sao lập, điều kiện gộp lại). Chỉ chạy #deliberate khi đơn vị mới tốn tiền thật hay khó quay lại; sản phẩm cấp trên giao và công cụ còn thiếu thì lập ngay.
2. Agent mới: propose_change resource agent op create (project này hoặc `project=<đơn vị>`); vai có sẵn mẫu (Thực thi, Kiểm soát, Nghiên cứu) thì chép từ `roles.md` cạnh skill này, kèm điều kiện tạo; vai khác tìm `agent_library` trước; quyền thấp nhất đủ dùng.
3. Đơn vị mới: chọn mẫu trong thư viện của office (`list` resource `project_pack`):

   | Mẫu | Dùng cho |
   |---|---|
   | `@agentoffice/startup/product` | một sản phẩm, repo code riêng |
   | `@agentoffice/startup/engineering` | hạ tầng, CI, bảo mật dùng chung |
   | `@agentoffice/startup/marketing` | khách hàng, thông điệp, nội dung, kênh |
   | `@agentoffice/startup/sales` | khách tiềm năng, cơ hội, doanh thu |
   | `@agentoffice/startup/support` | ticket, SLA, kho câu trả lời |
   | `@agentoffice/startup/unit` | khung trống khi không mẫu nào hợp |

   propose_create_project `pack=<mẫu>`, `parent` = đơn vị phụ trách, `name`, `description` = nhiệm vụ. Office tạo repo mới với gói và skill của mẫu: Trưởng đơn vị, Thực thi, Kiểm soát, bảng việc có sẵn việc khởi đầu.
4. Ghi `org`. Agent Kiểm soát của đơn vị: nhắc Chủ tịch chọn kết nối AI khác hãng.

## Gộp lại
- Lý do tách hết hiệu lực (việc song song xong, bộ nhớ không còn quá tải): tạm dừng agent, đưa kinh nghiệm của nó vào ghi nhớ của agent nhận lại; giải thể đơn vị (hỏi Chủ tịch).

## Thăng cấp / đổi vai
- Đổi role, hướng dẫn, quyền, hoặc đặt agent làm trưởng đơn vị. Office tự báo agent đó cập nhật ghi nhớ và lịch; kiểm câu trả lời của nó.
- Agent nhận chat "[Office] Your job changed": recall và sửa ghi nhớ cho đúng vai mới, chỉnh lịch theo nhịp của cấp mới, viết quy trình còn thiếu, báo cấp trên 5 dòng.
- Agent không chuyển được sang project khác: trưởng đơn vị mới là agent mới ở đó.

## Nhịp theo cấp (tự đặt lịch của mình cho đúng cấp)
| Cấp | Điều phối | Báo lên |
|---|---|---|
| Ban giám đốc | điều phối theo dữ liệu (bảng việc, lời giao, báo lên của đơn vị), quyết, chỉnh kế hoạch; bật khi có việc | Chủ tịch: khi xong mốc, cần quyết, số đổi đáng kể |
| Phòng ban / sản phẩm | như trên, bật khi nhận chỉ đạo (#intake); tự quyết việc trong phạm vi đơn vị | cấp trên: khi xong mốc (để biết); Chủ tịch: tiền thật, công khai, bên ngoài |
- Việc không chờ thế giới thật làm theo giờ chạy, không theo lịch người (skill `operations`). Hết việc thì tắt lịch của mình (`propose_automation` `enabled: false`), có việc lại bật.
- Lịch dưới một giờ nên có `only_if` theo bảng việc (`{"board": "changed"}`): không có gì đổi thì không chạy.
- Thông báo lên: send_to_chat project=<cấp trên>, 3-6 dòng; không có gì mới thì không gửi.
- Tự tạo, sửa lịch bằng propose_automation và quy trình bằng propose_change resource workflow (quyền `self.manage`): chat hoặc quy trình, cách nhau ít nhất 30 phút kèm trần chi phí ngày tối đa 5 USD; có `only_if` (không có việc thì không chạy) thì tới 5 phút, không cần trần. Ngoài khung đó thì đề xuất lên cấp trên.
- Thiếu cách làm lặp lại nhiều lần: viết thành quy trình, chạy thử với một việc nhỏ trước.

## Lịch và quy trình chỉnh theo việc
Lịch và quy trình là công cụ của bạn, không phải thứ cố định của mẫu. Mỗi lần giao ban hay xong một mốc, xem chúng còn hợp không, rồi tự chỉnh ngay (quyền `self.manage`, không cần duyệt):
- **Việc dồn, cần quay lại sớm:** dày lịch lên (tới 5 phút khi có `only_if`), hoặc hẹn một lượt bằng `schedule_next_run`.
- **Đang chờ thế giới thật, ít việc:** giãn lịch, hoặc tắt (`enabled: false`) và bật lại khi có việc.
- **Một cách làm lặp lại lần thứ hai:** viết thành quy trình; quy trình làm sai hay thừa bước thì sửa ngay.
- **Việc mới cần nhịp riêng** (thu số liệu, soát, nhắc khách…): thêm lịch, kèm `only_if` và trần chi phí.
- **Lịch, quy trình không còn dùng:** xóa, đừng để chạy cho có.
Ghi một dòng trong `decisions` khi đổi nhịp hay cách làm đáng kể, để lần sau biết vì sao.

## Nhân sự: CEO làm hết lúc đầu, chia dần theo bằng chứng

### Các giai đoạn
| Giai đoạn | Dấu hiệu | Tổ chức |
|---|---|---|
| 1. Khởi đầu | 1 mục tiêu, 1 sản phẩm | CEO quản lý hết; Thực thi, Kiểm soát, Nghiên cứu như gói |
| 2. Quản lý mảng | CEO quá tải (dưới đây), hoặc có ≥ 2 mục tiêu khác mảng (sản phẩm và bán hàng…) | thêm quản lý mảng ở Ban giám đốc (Giám đốc sản phẩm, Giám đốc tăng trưởng, Giám đốc vận hành…), mỗi người giữ một nhóm mục tiêu |
| 3. Đơn vị | một mảng có repo hay luồng việc riêng nhiều tuần | quản lý mảng thành trưởng đơn vị (project mới) |
| 4. Đồng CEO | vẫn quá tải sau khi chia, hoặc hai hướng lớn độc lập cần người cầm lái riêng | thêm CEO thứ hai (administrator như CEO, office tự cấp) |

### Đọc tải: `staff_load` (sau mỗi mốc lớn, ít nhất mỗi ngày chạy)
Một agent **quá tải** khi có từ hai dấu hiệu:
- gánh trên 60% chi phí hoặc lượt của project, và còn tăng;
- ghi nhớ cốt lõi gần giới hạn nạp, hoặc bị rút gọn nhiều lần (nhầm bối cảnh giữa các mảng);
- lượt lỗi trên 15%;
- điều phối trên 10 việc đang mở, hoặc lượt điều phối không kịp nghiệm thu, bỏ sót;
- lịch của nó chạy dày mà vẫn không kịp.

Một agent **rảnh** khi gần như không có lượt cả kỳ và không được giao việc nào: gộp việc của nó về người khác.

### Thêm quản lý mảng
1. `staff_load` và danh sách mục tiêu: chọn nhóm mục tiêu tách ra được (ít phụ thuộc phần còn lại). Ghi `decisions` (vì sao, điều kiện gộp lại).
2. `propose_change` resource agent op create: key và chức danh rõ (`head-of-product`…), hướng dẫn gồm: mục tiêu phụ trách, chỉ số, báo cáo cho ai, nhịp (mục Nhịp theo cấp), giới hạn tiền và rủi ro như CEO; quyền Vận hành với `self.manage` và `git.commit`: office tự cho chạy administrator như CEO (trưởng nhóm, ADR-214).
3. Bàn giao: trên bảng việc, đổi người theo dõi các việc thuộc mảng đó sang người mới (`burn_assign`); sửa `goals.owner`; gửi chat cho người mới: bối cảnh, việc dở, quyết định đã có, số liệu.
4. Lịch: người mới tự đặt điều phối của mình (`only_if.board`); bạn bỏ phần mảng đó khỏi lịch của mình nếu có.
5. Ghi `staff` (chức danh, mảng, báo cáo cho ai, `probation`) và `org`. Sau một mốc của mảng đó: đạt thì `permanent`, không thì gộp lại.

### Hai CEO cùng điều hành
- Chia theo **mục tiêu**, không theo ngày: mỗi CEO giữ một nhóm mục tiêu và các đơn vị phục vụ chúng; ghi rõ trong `staff.owns` và `goals.owner`.
- Một CEO giữ vai **trực**: báo cáo cho Chủ tịch là một bản chung, đọc báo cáo của người kia. Lịch không trùng: mỗi lịch một chủ.
- Quyết định chung (ngân sách, nhân sự, chiến lược) ghi `decisions`; hai bên không thống nhất thì chạy #deliberate với cả hai phương án, vẫn không xong thì hỏi Chủ tịch.
- Không sửa việc, lịch hay đơn vị của người kia; cần thì nhắn (send_to_chat) hoặc ghi trong báo cáo.
- CEO thứ hai là trưởng nhóm: office tự cho chạy administrator như CEO.

### Gộp lại
- Lý do tách hết (mảng xong, agent rảnh qua nhiều lượt): đổi người theo dõi việc trên bảng sang người nhận (`burn_assign`); tạm dừng agent (`propose_change` agent `enabled: false`); kinh nghiệm đáng giữ chuyển thành ghi nhớ của người nhận; `staff` đổi `paused`.

### Khi đổi vai
Đổi role, hướng dẫn hay quyền của một agent: office báo agent đó tự cập nhật ghi nhớ và lịch (mục Thăng cấp). Kiểm câu trả lời của nó, rồi ghi `staff`.
