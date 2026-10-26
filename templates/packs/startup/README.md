# Startup

Repo của một startup chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/startup` có sẵn trong office (ADR-199, ADR-204). Repo giữ **nội dung** của công ty: gói (`office.pack.json`), skill, tài liệu; sản phẩm thì nằm ở các đơn vị (project riêng). Động cơ là agent-office.

## Vòng lặp

```
goals ──► docs/roadmap.md ──► việc trên bảng việc ──► người điều phối chọn người làm, giao (delegate)
  ▲                                                     │ kiem-soat soát việc quan trọng
  │                                                     ▼
học (bai-hoc, tay-nghe) ◄── đo (metrics) ◄── nghiệm thu, gộp nhánh vào nhánh chính
```

Công ty dùng các phần sẵn có của office, không tự dựng lại:

| Việc của công ty | Phần của office |
|---|---|
| Bảng việc và phân công | Bảng việc của project (trang **Việc**: `burn_quest`, `burn_quests`, `burn_assign`, `burn_drop`); người điều phối chọn ai làm và giao bằng `delegate` kèm `task_id`, office tự chuyển việc sang đang làm rồi xong kèm kết quả; Burn trong trang đó làm liền một loạt việc khi được bật |
| Kiểm tra độc lập | #kiem-tra (so với lời giao nguyên văn, bằng dùng thật), #can-nhac (`kiem-soat`, model khác hãng) |
| Giữ ý định | Lời giao chép nguyên văn (`goals.request`, đầu `ROADMAP.md` của đơn vị); bị sửa thì qua #nhan-viec trước khi làm theo (skill `y-dinh`) |
| Tích lũy sản phẩm | Nghiệm thu rồi gộp thay đổi vào nhánh chính (`git_merge` khi làm trên nhánh riêng) |
| Nhịp làm việc | Ban giám đốc điều phối theo dữ liệu (lời giao, bảng việc, báo lên của đơn vị, `staff_load`), không hỏi báo cáo theo giờ; mỗi lượt một chat mới; điều phối quét 5 phút một lần, chỉ chạy khi bảng việc vừa đổi; lịch khác tự thêm khi cần; lịch dày chỉ chạy khi bảng việc vừa đổi (`only_if.board`) |
| Đo | Bảng dữ liệu `metrics`, `goals`, `ledger`; script theo lịch thu số (không tốn token), in `@@agent` khi cần người điều phối xem |
| Tổ chức | Đơn vị = project liên kết, có bảng việc riêng; CEO giao việc cho trưởng đơn vị, trưởng đơn vị chọn người làm |
| Nhân sự | `staff_load` (tải từng agent), người theo dõi việc (`burn_assign`) khi chia việc hay bàn giao, bảng `staff` (skill `to-chuc`) |
| Học | Ghi nhớ hai tầng; #better-harness sau mỗi mốc lớn |
| Thiếu gì thì tạo ra | Toàn quyền, không chờ duyệt: thiếu công cụ thì lập ngay một project làm công cụ đó (mẫu Sản phẩm, đội cần nó là người dùng thật); thiếu người thì tạo agent; office thiếu tính năng thì làm công cụ thay thế và gửi đề xuất (skill `nang-luc`). Chỉ hỏi Chủ tịch khi tiêu tiền thật hay đưa ra bên ngoài |

Nguyên tắc tổ chức vẫn là **gộp mặc định, tách khi có bằng chứng** (skill `to-chuc`). CEO làm gần hết việc quản lý lúc đầu; khi `staff_load` cho thấy dồn tải thì chia mục tiêu cho quản lý mảng, rồi thành đơn vị, thậm chí thêm một CEO cùng điều hành (skill `to-chuc`).

## Khởi đầu

Chỉ có **Tổng giám đốc** (`ceo`): người điều phối (kế hoạch, chọn người làm, nghiệm thu, đo, báo cáo). Quyền Vận hành + `self.manage`. Chỉ dẫn của nó bắt đầu bằng **Bạn là ai**: người điều phối, không phải người làm (ADR-203). Nó tự tạo đội từ thư viện agent của office (`office/thuc-thi`, `office/kiem-soat`, `office/nghien-cuu`) khi điều kiện của vai xảy ra (skill `to-chuc`, file `vai.md`), không tạo trước. Mọi agent của các mẫu đều có bản trong thư viện:

| Vai | Tạo khi | Quyền |
|---|---|---|
| **Thực thi** (`thuc-thi`) | có việc đầu tiên cần làm | Vận hành |
| **Kiểm soát** (`kiem-soat`) | lần đầu cần soát độc lập (#kiem-tra) hay phản biện (#can-nhac) | Chỉ đọc; **chọn kết nối AI khác hãng** |
| **Nghiên cứu** (`nghien-cuu`) | lần đầu cần thông tin bên ngoài | Chỉ đọc + Tra cứu web |

Chỉ dẫn của agent ngắn (vai, lời giao là chuẩn, giới hạn tiền và rủi ro, bảng "khi nào dùng quy trình hay skill nào"); cách làm nằm trong quy trình và skill (ADR-213). Mọi lời giao hay lời sửa đi qua một quy trình #nhan-viec gồm các bước: hiểu và giữ nguyên văn (không rõ thì hỏi lại) → nếu là lời sửa: vì sao mình không thấy, có phản biện → kế hoạch có phản biện → tổ chức và giao (CEO: lập dự án ở project liên kết mới, chỉ đạo trưởng dự án; đơn vị: tự lập đội từ thư viện, quy trình, lịch, chia việc) → bật lịch điều phối. Bảng việc (trang Việc) có sẵn "Kế hoạch theo mốc từ mục tiêu đầu vào" và "Bộ chỉ số và cách đo", CEO theo dõi. Burn (trong trang Việc) đặt sẵn nhưng tắt; người điều phối bật khi muốn một agent làm liền một loạt việc.

Muốn có sẵn đủ đội ngay từ đầu (Thực thi, Kiểm soát, Nghiên cứu, Burn đặt sẵn mà tắt): mẫu **Văn phòng giám sát** (`@agentoffice/startup/giam-sat`).

Skill trong `.claude/skills/` (mở khi cần): `y-dinh` (giữ đúng lời giao, "xong" là dùng thật, tự kiểm), `phat-trien` (dùng thử như người dùng, vấn đề thật, làm gì tiếp, đo), `van-hanh` (vòng lặp, nhịp, bảng việc, báo lên), `to-chuc` (đội, đơn vị, nhân sự), `quyet-dinh` (phương án có giá, đo lường), `nang-luc` (thiếu công cụ, kiến thức, người; nghiên cứu; đề xuất cho office).

Quy trình: `#nhan-viec`, `#giao-viec`, `#kiem-tra`, `#can-nhac`, `#better-harness` (Ban giám đốc).

Bảng dữ liệu: `goals`, `metrics`, `ledger` (thu chi), `decisions`, `reports`, `org`, `staff`, `office_requests`.

Tài liệu: `docs/roadmap.md`, `docs/chien-luoc.md`, `docs/chi-so.md`.

## Nhịp

Một tuần việc của người là khoảng một giờ của công ty: việc không chờ thế giới thật làm theo giờ chạy, không theo lịch người. **Lúc tạo không có lịch nào chạy.** Mẫu chỉ có sẵn lịch **Điều phối** (quét 5 phút, chỉ chạy khi bảng việc vừa đổi), đang tắt; #nhan-viec bật nó. Mỗi lượt mở chat mới, đọc trạng thái từ dữ liệu, đặt kết quả cạnh lời giao, nghiệm thu, giao việc tiếp, xem đội và chi phí (`staff_load`), báo khi xong mốc, rồi tự hẹn lượt sau theo ước lượng (`schedule_next_run`). Lịch khác CEO và quản lý tự thêm khi có lý do (skill `van-hanh`).

Đơn vị lập sau từ thư viện mẫu của office (`@agentoffice/startup/san-pham`, `ky-thuat`, `marketing`, `ban-hang`, `cskh`, `don-vi`): Trưởng đơn vị, Thực thi, Kiểm soát (Sản phẩm có thêm Dùng thử: một bản chỉ xong khi nó dùng thật được), bảng việc riêng có việc khởi đầu theo mảng; điều phối quét 5 phút khi bảng việc có việc, bật khi nhận chỉ đạo đầu tiên, báo lên khi xong mốc.

## Sau khi tạo công ty

Tạo từ dashboard (**Thư viện → Mẫu project → Startup**, hoặc Thêm project → Từ mẫu) và ghi **mục tiêu** vào ô Mục tiêu; đó là câu lệnh duy nhất (ADR-209). Office cấp quyền administrator (theo người tạo) cho agent mức Vận hành, ghi nguyên văn mục tiêu vào `goals.request` và gửi `#nhan-viec <mục tiêu>` cho Tổng giám đốc trong một chat ở chế độ Vận hành (ADR-213); từ đó CEO tự lập dự án, chỉ đạo, điều phối tới khi ra sản phẩm. Đơn vị và agent CEO tạo cũng tự chạy như vậy (ADR-207).

Vẫn hỏi Chủ tịch (kể cả khi là administrator): tiêu tiền thật, ký kết, đăng công khai, liên hệ bên ngoài lần đầu, push lên nhánh chính, giải thể đơn vị, quá 15 agent một project. Nên chọn kết nối AI khác hãng cho Kiểm soát khi CEO tạo nó.

## Bản đồ văn phòng

`ban-do/` là app nhỏ của công ty (Node 22.5+ hoặc có lệnh `sqlite3`, không cần cài gói) vẽ văn phòng: Phòng Giám đốc và các phòng ban (project liên kết), mỗi agent một bàn. Rảnh thì ngủ gật 💤, đang làm thì gõ phím ⌨️, giao việc thì tờ giấy 📄 bay sang bàn người nhận, báo lại 📋, nhắn sang chat khác 💬. App đọc dữ liệu office (`office.db`, chỉ đọc): ai đang có job chạy, job giao việc "A → B", tin gửi sang chat khác.

Chạy: **Vận hành** gợi ý tiến trình `ban-do` (từ `Procfile`), thêm rồi bật; hoặc `node ban-do/server.mjs`. Mở http://127.0.0.1:4710 (`PORT`, `HOST` đổi cổng và địa chỉ; `OFFICE_DB` hoặc `OFFICE_HOME` khi office để dữ liệu chỗ khác). Trang **Theo dõi** của office có widget **Bản đồ văn phòng** hiện app này.

## Lưu phần đã lớn lên

```bash
office project pack <công ty>     # ghi lại office.pack.json: config, ghi nhớ
git -C <thư mục công ty> commit -am "snapshot"
```

Clone repo này sang máy khác rồi `office init <thư mục>` là có lại công ty như lúc lưu.

## Giới hạn

- Agent chưa sửa hay xóa được ghi nhớ (chỉ thêm và đọc): "Dọn ghi nhớ" ghi bản đúng chồng lên.
- Bảng dữ liệu không đọc chéo đơn vị: việc và số liệu của đơn vị đi lên bằng báo cáo.
- Dashboard chưa có ô sửa `only_if.board` (agent đặt, dashboard giữ nguyên).
