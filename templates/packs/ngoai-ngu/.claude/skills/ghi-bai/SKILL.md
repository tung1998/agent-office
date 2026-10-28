---
name: ghi-bai
description: Ghi từ mới, mẫu ngữ pháp và bài học của người học ngoại ngữ vào sổ (bảng tu_vung, ngu_phap và file bai-hoc/), theo một quy ước tag thống nhất để ôn lại và ra đề về sau. Dùng mỗi khi người học gửi từ, câu, đoạn văn hay tài liệu mới.
---

# Ghi bài

Sổ học có hai phần: **bảng dữ liệu** (mỗi từ, mỗi mẫu ngữ pháp là một bản ghi, lọc được theo tag, hộp, ngày ôn) và **file bài học** (đọc lại như vở ghi). Bảng là nguồn chính; file bài học trỏ tới cùng các mục.

## 1. Tách cái đáng học
- Từ người học gửi: từ đơn, cụm từ, thành ngữ, cách nói cố định (collocation) đều là một mục `tu_vung`.
- Câu, đoạn văn: chọn từ mới và mẫu ngữ pháp đáng học theo trình độ trong `docs/ho-so.md`; không ghi từ người học chắc chắn đã biết (hỏi khi không rõ).
- Mỗi mẫu ngữ pháp là một mục `ngu_phap` (cấu trúc, không phải từng câu).

## 2. Không ghi trùng
`data_query` bảng `tu_vung` với `search` là từ đó (và `language`). Có rồi: bổ sung nghĩa, ví dụ mới, tag, không tạo mục mới. Từ đồng âm khác nghĩa thì là mục riêng, ghi rõ ở `meaning`.

## 3. Bảng `tu_vung` (data_upsert)
| Trường | Ghi gì |
|---|---|
| `word` | Từ hoặc cụm, dạng gốc (động từ nguyên thể, danh từ số ít) |
| `language` | Mã ngôn ngữ: `en`, `ja`, `zh`, `ko`, `fr`, `de`… |
| `reading` | Cách đọc: IPA, pinyin có thanh, furigana, romaja |
| `meaning` | Nghĩa tiếng Việt, ngắn; nhiều nghĩa thì đánh số |
| `part_of_speech` | `noun`, `verb`, `adj`, `adv`, `phrase`, `idiom`, `other` |
| `example` / `example_meaning` | Một câu ví dụ tự nhiên ở trình độ người học, và nghĩa |
| `notes` | Collocation, từ đồng nghĩa/trái nghĩa, sắc thái, chia động từ bất quy tắc, dễ nhầm với… |
| `level` | Trình độ của từ: `A1`…`C2`, `N5`…`N1`, `HSK1`…`HSK6`, `TOPIK1`…`TOPIK6` |
| `topics` | Chủ đề (xem quy ước tag) |
| `tags` | Tag còn lại (xem quy ước tag) |
| `source` | Lấy từ đâu: tên sách/bài/phim/link |
| `lesson` | Đường dẫn file bài học, ví dụ `bai-hoc/2026-10-28-du-lich.md` |
| `status` | `moi` khi vừa ghi |
| `box` | `1` khi vừa ghi |
| `next_review` | Ngày mai (ngày `YYYY-MM-DD`) |

Bảng `ngu_phap` tương tự: `pattern` (cấu trúc, ví dụ `V-ている`, `have + V3`), `meaning`, `structure` (cách ghép, chia), `examples` (2-3 câu kèm nghĩa), `notes` (khi nào dùng, phân biệt với mẫu gần giống), `level`, `topics`, `tags`, `lesson`, `status`, `box`, `next_review`.

## 4. Quy ước tag
Tag viết thường, không dấu, nối bằng `-`. Hai trường:
- **`topics`**: chủ đề nội dung, chọn từ danh sách sẵn có trước khi đặt cái mới: `chao-hoi`, `gia-dinh`, `cong-viec`, `du-lich`, `an-uong`, `mua-sam`, `suc-khoe`, `giao-duc`, `cong-nghe`, `moi-truong`, `xa-hoi`, `kinh-te`, `cam-xuc`, `thoi-gian`, `nha-cua`, `giao-thong`, `the-thao`, `van-hoa`.
- **`tags`**: tag có tiền tố, để lọc khi ôn và ra đề:
  - `bai:<YYYY-MM-DD>` buổi học ghi mục này (bắt buộc)
  - `nguon:<sach|phim|bao|podcast|hoi-thoai|de-thi>` loại nguồn
  - `ky-thi:<ielts|toeic|jlpt|hsk|topik|delf|…>` khi mục thuộc phạm vi kỳ thi mục tiêu
  - `ky-nang:<nghe|noi|doc|viet>` khi mục gắn với một kỹ năng
  - `loi:<loai>` do Chấm bài thêm khi người học sai: `loi:nghia`, `loi:chinh-ta`, `loi:cach-doc`, `loi:chia-dong-tu`, `loi:gioi-tu`, `loi:trat-tu`, `loi:nham-tu-gan`
  - `can-kiem` khi chưa chắc về nghĩa hay ví dụ (Chấm bài soát rồi bỏ tag)
  - `uu-tien` khi người học muốn ôn nhiều

## 5. File bài học
Mỗi buổi một file `bai-hoc/<YYYY-MM-DD>-<chu-de>.md` (cùng ngày học thêm thì ghi tiếp vào file đó):

```markdown
# <Chủ đề> · <ngày>
Nguồn: <…>

## Từ vựng
| Từ | Cách đọc | Nghĩa | Ví dụ |
|---|---|---|---|

## Ngữ pháp
### <mẫu>
Cấu trúc, ý nghĩa, ví dụ, phân biệt với…

## Giải thích thêm
Văn hóa, cách dùng, điều dễ nhầm.

## Tự kiểm tra
3-5 câu hỏi nhanh (đáp án ở cuối file, sau dòng `---`).
```

## 6. Báo lại người học
Đã ghi bao nhiêu từ, mẫu ngữ pháp nào, file nào; từ nào đã có (đã bổ sung gì); điều gì chưa chắc.
