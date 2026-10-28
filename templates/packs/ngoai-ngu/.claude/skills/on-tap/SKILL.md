---
name: on-tap
description: Ôn tập ngoại ngữ theo lặp lại ngắt quãng (hộp Leitner): chọn từ và mẫu ngữ pháp đến hạn từ bảng tu_vung, ngu_phap theo tag, soạn bài ôn nhiều dạng, chấm và cập nhật hộp, ngày ôn, tag lỗi. Dùng khi ôn hằng ngày, khi người học hỏi ôn hay khi chấm bài ôn.
---

# Ôn tập

## Lịch ôn (hộp Leitner)
Mỗi mục có `box` 1-5 và `next_review`.

| Hộp | Ôn lại sau |
|---|---|
| 1 | 1 ngày |
| 2 | 3 ngày |
| 3 | 7 ngày |
| 4 | 14 ngày |
| 5 | 30 ngày |

- Trả lời đúng: lên một hộp (tối đa 5), `next_review` = hôm nay + khoảng của hộp mới. Đúng ở hộp 5 hai lần liền: `status` = `thuoc`.
- Sai: về hộp 1, `next_review` = ngày mai, `status` = `dang-hoc`, thêm tag `loi:<loại>`. Sai từ 3 lần trở lên (`wrong` ≥ 3): `status` = `kho`.
- Gần đúng (sai chính tả nhỏ, thiếu dấu thanh): giữ hộp, ôn lại sau 1 ngày.
- Luôn cập nhật `last_review`, `correct` hoặc `wrong`.

## Chọn mục để ôn
1. Đến hạn: `data_query` `tu_vung` với `next_review` `lte` hôm nay, `status` `ne` `thuoc`, sắp theo `next_review`. Tương tự `ngu_phap`.
2. Ưu tiên: tag `uu-tien`, `status` `kho`, tag `loi:*`.
3. Người học chọn phạm vi ("ôn chủ đề du lịch", "ôn bài tuần trước"): lọc theo `topics` (`contains`), `tags` `bai:<ngày>`, `level`.
4. Một buổi ôn 15-25 mục (theo thời gian trong `docs/ho-so.md`); quá nhiều mục đến hạn thì lấy mục quá hạn lâu nhất trước, còn lại để mai.

## Dạng bài
Trộn để nhớ chắc, mỗi mục một câu:
- Nghĩa → từ (gõ lại từ, có cách đọc), từ → nghĩa
- Điền vào chỗ trống trong câu mới (không dùng lại câu ví dụ đã ghi)
- Chọn đáp án đúng (4 lựa chọn, nhiễu là từ gần nghĩa hoặc gần hình)
- Chia động từ / chọn dạng đúng theo mẫu ngữ pháp
- Sửa lỗi câu sai, dịch câu ngắn Việt → ngôn ngữ học
- Nghe/nói: đưa câu để người học đọc to, hoặc chép chính tả (ghi rõ là tự đánh giá)

## File và bảng
- Bài ôn: `bai-tap/<YYYY-MM-DD>-on-tap.md`; đáp án: `bai-tap/<YYYY-MM-DD>-on-tap-dap-an.md`. Mỗi câu ghi mục nó kiểm tra: `(tu_vung: <word>)` hoặc `(ngu_phap: <pattern>)` trong file đáp án.
- Bản ghi `bai_tap`: `kind` = `on-tap`, `file`, `items`, `status` = `chua-lam`.
- Đưa câu hỏi vào chat theo thứ tự, không kèm đáp án. Người học trả lời trong chat hoặc ghi vào file bài.

## Sau khi chấm
Tóm tắt: đúng bao nhiêu, mục lên hộp, mục về hộp 1 kèm lỗi, tag yếu (`weak_tags`), số mục đến hạn ngày mai.
