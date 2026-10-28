---
name: tri-thuc
description: Wiki của project (`docs/wiki/`) như LLM Wiki của Karpathy: trạng thái thật có bằng chứng, nhật ký quyết định (cái nào thay cái nào, vì sao), hiểu biết đã tích lũy; đọc trước khi quyết, ghi sau mỗi nghiệm thu hay đổi hướng, lint bằng script, soát hai bên/ba bên.
---

# Tri thức: wiki của project

Ghi nhớ của agent giữ **luật làm việc**. Wiki giữ **điều công ty đã biết và đã quyết**: một chỗ, có nguồn, mọi agent và Chủ tịch đọc được (file markdown trong repo, git giữ lịch sử). Mỗi lượt điều phối đọc wiki thay vì dựng lại trạng thái từ chat và tóm tắt việc.

## Cấu trúc
```
docs/wiki/
  index.md        mục lục: mỗi trang một dòng (link, một câu nói trang đó có gì)
  trang-thai.md   mỗi mốc: Đang làm / Đã xong / Hỏng, kèm bằng chứng
  quyet-dinh.md   nhật ký quyết định, mới nhất trên cùng
  log.md          dòng thời gian: ## [YYYY-MM-DD] loại | tiêu đề
  hieu-biet/*.md  điều đã biết: nghiên cứu, giới hạn kỹ thuật, bài học về sản phẩm, khách hàng
```
Chưa có thì tạo: `node .claude/skills/tri-thuc/lint.mjs --init`. Đơn vị dùng wiki trong repo của chính nó.

## Quy ước
- **Đã xong** = đã gộp vào nhánh chính. Mỗi dòng có mã commit (`- <mốc>: <kết quả dùng thật> — commit abc1234, soát: <ai, PASS>`). Code còn trong worktree hay nhánh riêng là **Đang làm**, không bao giờ là xong. Mốc của đơn vị ghi kèm repo của nó: `commit abc1234 @ /đường/dẫn/repo-đơn-vị` (lint kiểm trong repo đó).
- **Quyết định** là một mục `### YYYY-MM-DD <tiêu đề>` với các dòng:
  - `Lời gốc:` trích nguyên văn lời Chủ tịch/cấp trên mà nó phục vụ;
  - `Quyết:` điều đã chọn;
  - `Vì sao:` lý do, số liệu kèm nguồn;
  - `Thay:` tiêu đề quyết định cũ bị thay (nếu có); mục cũ giữ nguyên, thêm dòng `Bị thay bởi: <tiêu đề mới>`;
  - `Bỏ:` các hướng/việc không làm nữa (để đóng quest theo hướng đó);
  - `Soát:` ai soát, kết luận.
- **Hiểu biết**: mỗi trang một chủ đề, mỗi khẳng định có nguồn (file:dòng, URL, quest, commit). Thông tin mới trái thông tin cũ: sửa trang, ghi `Trước đây: … (sai vì …)`, không xóa im lặng.
- Mỗi lần sửa wiki: thêm một dòng `log.md` và cập nhật `index.md` nếu thêm trang.

## Ba thao tác
**Nạp (ingest)**, trong cùng lượt với sự kiện:
- Nghiệm thu xong một việc → `trang-thai.md` (sau khi soát hai bên, mục dưới), kết quả nghiên cứu/thử nghiệm → `hieu-biet/`.
- Lời giao mới, lời sửa, đổi hướng → `quyet-dinh.md` (sau khi soát ba bên), rồi `burn_drop` mọi quest theo hướng trong `Bỏ:`.
- Một việc thường chạm 2–5 trang. Ghi ngắn, có nguồn.

**Hỏi (query)**: đầu mỗi lượt điều phối, báo cáo, tham mưu: đọc `index.md`, `trang-thai.md`, các mục `quyet-dinh.md` liên quan, rồi mới đọc bảng việc và báo lên. Ý kiến đưa ra trích trang wiki hay nguồn; điều wiki không có ghi là chưa biết. Câu trả lời có giá trị lâu dài (so sánh, phân tích) lưu thành trang `hieu-biet/`.

**Lint**: `node .claude/skills/tri-thuc/lint.mjs` (không tốn token) đầu mỗi lượt điều phối, báo cáo, nghiệm thu. Nó bắt: "Đã xong" không có commit hoặc commit chưa gộp, link hỏng, trang không nằm trong index, quyết định thiếu `Lời gốc`/`Vì sao`, `Thay:` trỏ tới quyết định không có, wiki cũ hơn code. Sau đó tự đối chiếu bảng việc: quest còn mở thuộc hướng đã `Bỏ:` → `burn_drop`; hai quest cùng kết quả → `burn_drop` một cái. Sửa hết trước khi làm việc khác.

## Soát nhiều bên, theo tầng
| Tầng | Khi nào | Cách |
|---|---|---|
| 0 | luôn | lint ở trên |
| 2 bên | ghi một mốc vào **Đã xong** | người làm tự kiểm + #kiem-tra (`kiem-soat`, khác hãng) soát **độc lập** theo bằng chứng (commit đã gộp, dùng thật). Hai bên lệch: để Đang làm, ghi điều lệch |
| 3 bên | ghi một **quyết định** đổi hướng, chọn phương án sản phẩm, hay khi tầng 2 bên không thống nhất | người điều phối viết đề xuất; #can-nhac để `kiem-soat` phản biện; bên thứ ba (agent khác cả hai, hoặc #review-feature-2-ben) đối chiếu riêng với `Lời gốc`. Người điều phối tổng hợp, ghi cả điều bị phản biện đổi |

Mỗi bên soát không xem kết luận của bên kia trước. Ghi kết luận từng bên vào dòng `Soát:` hay `log.md`. Việc nhỏ trong một mốc không cần soát riêng: soát ở mốc.
