---
name: growth
description: Phân tích để phát triển sản phẩm: dùng thử như người dùng, tìm vấn đề thật, xem đối thủ, chọn việc tiếp theo, làm bản nhỏ nhất để thử, đo xem có giải được vấn đề không. Dùng khi lập kế hoạch và sau mỗi mốc.
---

# Phát triển sản phẩm

Việc của đội sản phẩm là giải vấn đề của người dùng, không phải đóng checklist (test, gói, hồ sơ). Mỗi vòng đi qua năm bước; bước nào thiếu bằng chứng thì chưa sang bước sau.

## 1. Dùng thử như người dùng
- Người dùng là ai, dùng ở đâu (máy, trình duyệt, tài khoản, trang, dữ liệu), để làm việc gì: lấy từ lời giao nguyên văn (skill `intent`), không đoán.
- Tự dùng luồng chính từ đầu như người dùng lần đầu, ở đúng chỗ họ dùng. Ghi từng chỗ vướng, chậm, khó hiểu, kèm ảnh hay bước tái hiện.
- Chỗ chỉ người thật vào được (đăng nhập, cài đặt): chuẩn bị sẵn, xin người đúng một bước ngắn, rồi tự đọc kết quả.

## 2. Tìm vấn đề thật
- Nguồn: lần dùng thử, lời người dùng (phản hồi, ticket, CSKH, Bán hàng), số liệu dùng. Mỗi vấn đề ghi: ai gặp, lúc nào, mất gì (thời gian, tiền, bỏ dở), bằng chứng.
- Tách vấn đề với giải pháp: "người dùng phải ẩn tay quảng cáo mỗi lần mở trang" là vấn đề; "thêm preset" là giải pháp.

## 3. Xem đối thủ và cách người dùng đang làm tạm
- Người dùng đang giải vấn đề đó bằng gì (sản phẩm khác, làm tay, bỏ qua)? Đối thủ làm tốt gì, thiếu gì? Giao agent `researcher` (skill `capabilities`), có nguồn và ngày.
- Mục đích là tìm chỗ sản phẩm mình hơn, không phải chọn tên hay làm hồ sơ.

## 4. Chọn việc tiếp theo
- Xếp vấn đề theo: bao nhiêu người gặp × mất bao nhiêu × mình làm được nhanh không. Chọn ít việc, ghi lý do không chọn phần còn lại.
- Mỗi việc là bản nhỏ nhất kiểm được giả định (một luồng, một trang, một preset), có câu "xong khi người dùng làm được … ở …".
- Giả định lớn (người dùng có cần không, có trả tiền không): thử rẻ trước khi làm to (bản mẫu, hỏi người, đo một tuần).

## 5. Đo và học
- Sau mỗi bản: dùng thử lại (bước 1), so với vấn đề đã chọn. Giải được thì ghi số (skill `decisions`); không thì ghi vì sao và đổi hướng, không làm thêm cho đủ.
- Kết quả mỗi vòng vào `reports` (kind `product`): đã thử gì, thấy gì, chọn gì tiếp, vì sao.
