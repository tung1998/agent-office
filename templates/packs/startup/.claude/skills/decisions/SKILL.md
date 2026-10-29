---
name: decisions
description: Quyết định có giá và đo kết quả: phương án, chi phí, lợi ích, rủi ro, điều kiện dừng; tổng hợp số cho cấp trên; chỉ số chính và dẫn, bảng metrics, thu số tự động bằng script, đọc xu hướng.
---

# Quyết định bằng số

## Tính toán trước khi quyết
- Trước mỗi quyết định tốn tiền thật hay khó quay lại, viết ra:
  1. **Vấn đề**: thiếu gì, ảnh hưởng mục tiêu nào, đo bằng gì.
  2. **Phương án**: ít nhất hai, luôn có "không làm / làm tạm bằng cái có sẵn".
  3. **Tính toán**: chi phí (token/USD mỗi ngày, thời gian tới lúc dùng được, công duy trì), lợi ích (chỉ số nào tăng bao nhiêu, khi nào), thời gian hoà vốn.
  4. **Rủi ro** và **điều kiện dừng**: dấu hiệu nào thì bỏ, ngân sách tối đa.
- Chọn phương án rẻ nhất đạt mục tiêu; ưu tiên làm thử nhỏ, đo, rồi mới mở rộng.
- Quyết định lớn (cấp administrator, chi tiền thật, thay đổi khó quay lại): chạy #deliberate để agent khác hãng phản biện trước. Tạo project, đơn vị, agent, công cụ để lấp chỗ thiếu không cần #deliberate (skill `capabilities`): làm ngay, ghi `decisions`.
- Ghi `decisions` kèm ước tính. Sau mỗi mốc so ước tính với thực tế; lệch nhiều thì ghi bài học (topic `lessons`) và chỉnh cách ước tính.

## Tham mưu
- Lấy số từ `goals`, `metrics`, `ledger`, `reports`, `decisions` (data_query) và bảng việc (`burn_quests`); ghi nguồn và ngày; ước đoán ghi là ước đoán.
- Mỗi phương án: chi phí, lợi ích (chỉ số nào, bao nhiêu, khi nào), rủi ro, điều kiện dừng. Luôn có phương án "không làm".
- Trình bày: khuyến nghị trước, bảng phương án, điều cần Chủ tịch quyết.

## Đo lường
- Mỗi mục tiêu có **một chỉ số chính** (đo kết quả: doanh thu, khách trả tiền…) và **2-4 chỉ số dẫn** (đo thứ làm chỉ số chính nhích: lượt dùng thử, tỉ lệ chuyển đổi, thời gian giao…). Định nghĩa ở `docs/metrics.md`.
- Mỗi số đo là một dòng `metrics`: `date`, `metric`, `value`, `goal`, `unit` (đơn vị của công ty), `source` (lấy từ đâu). Không sửa số cũ: thêm dòng mới.
- `goals.current` là số mới nhất của chỉ số chính.

### Thu số tự động (không tốn token)
- Số lấy được bằng lệnh (API, file, database, log): viết script nhỏ (skill `capabilities`), rồi `propose_automation` action `script` theo lịch hằng ngày, in số ra. Người đọc script rồi bật.
- Script cần AI xử lý tiếp thì in dòng `@@agent …`; phát hiện việc cần làm thì cũng in `@@agent` kèm mô tả để người điều phối ghi lên bảng việc (`burn_quest`) và chọn người làm.
- Số chỉ lấy tay được (số liệu từ đối tác…): ghi nguồn, ngày; nhắc trong báo cáo khi thiếu.

### Đọc xu hướng
- `data_query metrics` theo `metric`, sắp theo `date`: so với mốc trước và với lúc bắt đầu.
- Chỉ số dẫn lên mà chỉ số chính đứng yên: sai giả định, xem lại chiến lược (#deliberate).
- Báo cáo: số cũ → số mới (±%), nguồn; không có số thì nói không có, không ước cho đẹp.
