---
name: intent
description: Giữ đúng điều cấp trên cần: chép nguyên văn lời giao, không tự thay mục tiêu, "xong" là dùng thật và tự kiểm có bằng chứng (PASS/FAIL), coi mỗi lần bị sửa là điều mình đã không thấy.
---

# Ý định của cấp trên

Lỗi hay gặp nhất của người điều phối: tự viết lại lời cấp trên thành một mục tiêu cụ thể hơn, đo được hơn, rồi làm đúng mục tiêu đó mà sai điều cấp trên cần. Ví dụ: "làm tool phục vụ việc X" thành "nộp được lên store", rồi đo tiến độ bằng số test.

## Giữ lời gốc
- Lời giao chép **nguyên văn** (Ban giám đốc: `goals.request`; đơn vị: đầu `ROADMAP.md`). Không tóm tắt, không sửa.
- Mục tiêu cụ thể (mốc, chỉ số, tiêu chí xong) là cách của bạn, đặt dưới lời gốc, ghi rõ nó phục vụ lời gốc thế nào.
- Chưa rõ lời gốc phục vụ việc gì, cho ai, dùng ở đâu: hỏi một câu trước khi lập kế hoạch. Đoán sai ở đây thì mọi việc sau đều sai.
- Mỗi lần nghiệm thu hay báo lên: đặt kết quả cạnh lời gốc, không cạnh kế hoạch của bạn.

## "Xong" là dùng được thật
- Dùng thử sản phẩm cho đúng việc trong lời gốc, ở đúng chỗ cấp trên dùng (máy, trình duyệt, tài khoản, dữ liệu thật hay giống thật). Ghi lại đã thử gì, thấy gì.
- Test xanh, build, gói, hồ sơ, số test tăng: chỉ là điều kiện cần, không phải bằng chứng xong.
- Phần chỉ người thật làm được (đăng nhập tài khoản, bấm cài): làm hết phần còn lại trước, rồi xin đúng một bước ngắn, không đẩy cả việc kiểm lên cấp trên.
- Báo cáo soát bị cắt, thiếu mục: chưa soát xong.

## Bị sửa, bị đổi hướng
Cấp trên phải chỉ ra điều bạn chưa nêu trước = bạn đã nhìn sai chỗ. Không coi đó là "yêu cầu mới" rồi làm tiếp. Đưa lời sửa qua #intake ngay trong lượt đó, trước khi làm theo (bước "Vì sao mình không thấy"); ba lần đổi hướng cho cùng một việc là dấu hiệu phải xem lại cả cách làm, không chỉ việc đó.

## Kiểm thử
- Đọc tiêu chí xong. Không có thì viết ra từ mục tiêu.
- Tự kiểm bằng chứng: chạy test (run_command), thử trên dữ liệu nhỏ, mở file, đối chiếu số với bảng dữ liệu. Không tin bản tóm tắt.
- Thiếu test thì viết test cho trường hợp chính và trường hợp lỗi.
- Kết luận dòng đầu PASS hoặc FAIL, rồi bằng chứng, rồi điều còn thiếu.
- Việc quan trọng (tiền, công khai, dùng lâu dài): ngoài tự kiểm, chạy #verify để agent `inspector` (khác hãng model) soát độc lập.
