---
name: operations
description: Vòng lặp vận hành và nhịp của nó (kế hoạch → phân công → nghiệm thu → đo → học, tính bằng giờ chạy của AI), lịch, báo lên; bảng việc: viết việc, chọn người, giao bằng delegate kèm task_id, nghiệm thu, việc hỏng, khi nào bật Burn.
---

# Vận hành: một vòng lặp đưa công ty đi lên

```
goals ──► docs/roadmap.md ──► việc trên bảng việc ──► người điều phối chọn người làm, giao (delegate)
  ▲                                                     │ inspector soát việc quan trọng
  │                                                     ▼
học (lessons, craft) ◄── đo (metrics) ◄── nghiệm thu, gộp nhánh vào nhánh chính
```

Mỗi vòng phải làm một con số trong `metrics` nhích lên. Vòng nào không làm số nào nhích thì dừng lại hỏi: sai kế hoạch, sai người, hay sai chỉ số.

## Ai làm gì
| Bước | Ai | Công cụ |
|---|---|---|
| Kế hoạch | người điều phối (CEO, trưởng đơn vị, trưởng nhóm) | `docs/roadmap.md` (đơn vị: `ROADMAP.md`), bảng việc (`burn_quest`) |
| Phân công | người điều phối **quyết định ai làm** | `staff_load`, `delegate`, `send_to_chat project=` |
| Làm | agent được giao (`implementer` và các agent theo vai) | worktree của nó |
| Soát | `inspector` (model khác hãng) cho việc quan trọng | #verify |
| Nghiệm thu | người điều phối | `git_diff`, `run_command`, `propose_action git_merge` |
| Đo | người điều phối, script theo lịch | `metrics`, `goals.current`, `ledger` |
| Học | mọi agent | wiki `docs/wiki/` (điều đã biết, đã quyết, trạng thái thật; skill `knowledge`), ghi nhớ `lessons`, `craft`, `repeat-mistakes` (luật làm việc) |

Burn không nằm trong vòng này: nó tự nhận việc, không phân công được. Người điều phối chỉ dùng khi chủ động muốn một agent làm liền một loạt việc cùng loại (mục Bảng việc bên dưới).

## Nhịp: tính bằng giờ chạy, không bằng lịch người
Một tuần việc của người là khoảng một giờ của công ty. Lịch người (ngày, tuần, giờ hành chính) chỉ dùng cho phần phải chờ thế giới thật: khách trả lời, số liệu cần ngày để có, Chủ tịch duyệt.

**Lúc tạo công ty không có lịch nào chạy.** Khi Chủ tịch giao việc, CEO đưa lời đó qua #intake, quy trình đó bật lịch **Điều phối**; đơn vị nhận chỉ đạo cũng qua #intake, bật **Điều phối đơn vị**. Mỗi lượt điều phối mở chat mới, đọc trạng thái từ dữ liệu (lời giao nguyên văn, bảng việc, `goals`, roadmap), không từ trí nhớ của chat: nghiệm thu việc vừa về, đặt kết quả cạnh lời giao, giao việc tiếp. Không hỏi báo cáo từng đơn vị theo giờ, không duyệt việc trong phạm vi đơn vị: đơn vị tự quyết, tự báo lên khi xong mốc hoặc khi cần Chủ tịch. Lịch điều phối quét 5 phút một lần nhưng chỉ chạy khi bảng việc vừa đổi (`only_if.board: changed`: việc mới, người làm vừa xong, việc chờ nghiệm thu): người làm xong là lượt sau chạy ngay; việc nằm chờ (chờ Chủ tịch, chờ thế giới thật) không làm lịch chạy lại, không tốn gì. Cần quay lại khi bảng không đổi (chờ thế giới thật) thì hẹn bằng `schedule_next_run`. Hết việc, hoặc mọi việc còn lại chờ cấp trên: không tắt lịch (không có việc thì nó không chạy); xin chỉ đạo cấp trên (đơn vị → Ban giám đốc, Ban giám đốc → Chủ tịch) kèm đề xuất 1-3 việc tiếp; chỉ đạo về thành việc trên bảng thì lịch tự chạy lại.

Lịch khác **chỉ thêm khi có lý do**, CEO và các quản lý tự đặt (`propose_automation`):

| Khi nào cần | Lịch gợi ý |
|---|---|
| Chủ tịch muốn báo cáo định kỳ | báo cáo theo mốc, hoặc mỗi sáng (chat, ngắn) |
| Có nguồn số liệu đọc được bằng lệnh | thu số bằng script theo lịch (không tốn token), in `@@agent` khi cần người điều phối xem (skill `decisions`) |
| Sản phẩm đã có người dùng | soát sản phẩm như khách hàng sau mỗi bản phát hành |
| Ghi nhớ bắt đầu dày | dọn ghi nhớ sau mỗi mốc lớn |
| Chạy đã nhiều ngày | rà soát: quyết định vs kết quả, tách/gộp, #better-harness |

Lịch dày (dưới một giờ) nên có `only_if` theo bảng việc (`board`) và `limits.daily_cost_usd`. Đơn vị: điều phối cùng nhịp, bật khi nhận chỉ đạo đầu tiên.

## Báo lên
Việc trong phạm vi của mình thì tự quyết, không gửi lên xin duyệt. Gửi cấp trên (`send_to_chat project=<cấp trên>`) khi xong mốc (để biết) hoặc khi cần Chủ tịch, ngắn:
- **Kết quả so với lời giao:** đã dùng thật thế nào, thấy gì; không chỉ số test.
- **Việc:** xong (kèm bằng chứng), đang làm (ai), vướng (cần gì).
- **Đội và chi phí:** ai làm gì, ai tốn nhiều (`staff_load`), thay đổi người hay cách chia việc và vì sao.
- **Cần Chủ tịch quyết:** chỉ tiền thật, công khai, cam kết bên ngoài, push nhánh chính; mỗi việc kèm đủ thông tin để quyết một lần.

## Khi vòng lặp kẹt
- Việc hỏng hai lần: viết chưa rõ, quá to, hoặc sai người. Chia nhỏ, ghi rõ `done_when`, đổi người.
- Soát trả lại nhiều: đọc điều thiếu, đưa thành quy tắc vào việc sau; lỗi lặp thì thêm lệnh kiểm tra cho người làm.
- Một người gánh hết: `staff_load`, chia việc hoặc thêm người (skill `organization`).
- Số không nhích sau vài mốc: chạy #deliberate với số liệu, đổi kế hoạch chứ không đổi chỉ số cho đẹp.

## Bảng việc và phân công

Mọi việc của project đi qua **bảng việc** (trang Việc trên dashboard, người dùng xem ở đó): ghi việc bằng `burn_quest`, xem bằng `burn_quests` (trạng thái, người theo dõi, kết quả), đổi người theo dõi bằng `burn_assign`, bỏ việc trùng hay việc kế hoạch mới đã thay bằng `burn_drop` (kèm lý do). Người điều phối (lúc đầu là Tổng giám đốc, về sau trưởng đơn vị hoặc trưởng nhóm) **quyết định ai làm từng việc**; người theo dõi (`owner`) là người nghiệm thu.

### Viết việc
- Một việc là một kết quả kiểm được, một agent làm xong trong một lượt dài. To hơn: chia thành nhiều việc theo thứ tự.
- `title` ngắn, bắt đầu bằng động từ. `detail` đủ để làm mà không phải hỏi lại:
  ```
  Mục tiêu: <mục tiêu trong goals, chỉ số nào nhích>
  Kết quả: <cái gì tồn tại khi xong>
  Ở đâu: <file, thư mục, tài liệu cần đọc>
  Ràng buộc: <ngân sách, điều cấm; điều cần duyệt thì chỉ chuẩn bị>
  Xong khi: <tiêu chí đo được và cách kiểm: lệnh test, cách dùng thử, số cần thấy>
  ```

### Chọn người và giao
1. Chọn theo **vai** (role của agent: `list` resource agent) và **tải** (`staff_load`). Không ai hợp: tạo agent đúng vai (skill `organization`); người điều phối không tự làm.
2. Giao: `delegate` (agent cùng project) với `task_id` = id của việc trên bảng: office tự chuyển việc sang **đang làm** (ghi tên người làm), khi người làm trả lời thì sang **xong** kèm câu trả lời làm kết quả (không trả lời được thì **thất bại**). Việc của đơn vị khác: `send_to_chat project=<đơn vị>`, trưởng đơn vị ghi lên bảng của đơn vị và chọn người.
   - **Mỗi agent làm một việc một lúc:** agent đang làm việc được giao thì chưa nhận việc khác (office từ chối); việc tiếp theo của nó để chờ trên bảng, giao khi nó trả kết quả. Muốn chạy song song thì giao cho nhiều agent; thường xuyên phải chờ một người là dấu hiệu cần thêm người (skill `organization`).
   - Hai việc sửa cùng file không giao song song (diff sẽ xung đột).
3. Kết quả trở về chính lượt điều phối của bạn.

### Nghiệm thu
1. Chạy lint wiki (skill `knowledge`). Đọc câu trả lời của người làm; mở thay đổi (`git_diff`, file), chạy kiểm tra (`run_command`). Việc quan trọng (tiền, dữ liệu, công khai, dùng lâu dài): #verify để `inspector` soát.
2. Đạt: commit đúng các file của việc đó (`propose_action git_commit`, liệt kê file, message nói việc gì). Thay đổi nằm trên một nhánh riêng thì soát rồi `propose_action git_merge`. Không để thay đổi của nhiều việc dồn lại chưa commit.
3. Không đạt: thêm việc sửa lên bảng (`burn_quest`, nêu điều thiếu cụ thể) và giao lại cùng người hoặc người khác.
4. Số đổi thì ghi `metrics`/`goals`. Mốc xong (đã gộp, soát hai bên) thì ghi `docs/wiki/status.md` kèm commit; điều mới biết vào `docs/wiki/knowledge/`.

### Việc hỏng
- Hỏng hai lần: việc viết chưa rõ hoặc quá to, hoặc giao sai người. Chia nhỏ, ghi rõ "Xong khi", đổi người.
- Cần thông tin bên ngoài: giao `researcher`, rồi giao lại kèm kết quả.
- Không đáng làm nữa: bỏ qua trên bảng (nút Bỏ qua, hoặc ghi lý do khi báo cáo). Kiểu hỏng lặp lại: ghi nhớ topic `lessons`.

### Burn: làm một loạt việc
Burn là tính năng của bảng việc: một agent tự nhận lần lượt các việc đang chờ trên bảng và làm liền. Nó **không chọn người**, nên dùng khi bạn có một loạt việc cùng loại cho cùng một người (dọn lỗi, viết test, sửa tài liệu): đặt agent cho Burn (`propose_change` resource burn `agent_id`, thường là `implementer`), bật (`run=on`), nghiệm thu nhánh `burn/…` rồi gộp, tắt khi xong (`run=finish`). Việc bạn đã giao bằng `delegate` thì Burn không đụng tới.
