# Phân tích thị trường

Repo phân tích thị trường và giao dịch giấy của một nhà đầu tư, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/trading`. Mô hình lấy từ TradingAgents, ai-hedge-fund, FinRobot, FinMem (`sources/references.md`).

> **Chỉ giao dịch giấy.** Office không đặt lệnh thật, không nối sàn hay công ty chứng khoán, không giữ key của họ. Phân tích là để tham khảo, không phải lời khuyên đầu tư; người quyết định là bạn.

## Luồng

```
danh sách theo dõi (watchlist) ──► bản tin sáng
          │
          ▼
phân tích một mã: cơ bản · kỹ thuật · tin tức · tâm lý (mỗi số có nguồn)
          │
          ▼
tranh biện: phe tăng ⇄ phe giảm ──► tín hiệu (signals): mua/giữ/bán/theo dõi, độ tin cậy, cắt lỗ, mục tiêu
          │
          ▼
lệnh giấy (paper_trades) theo quy tắc rủi ro ──► theo dõi cuối ngày ──► đánh giá tuần ──► bài học (lessons)
                                                                                         │
                                         lần phân tích sau đọc lại  ◄────────────────────┘
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Theo dõi | "Theo dõi FPT, HPG, chú ý FPT dưới 110" | Quản lý danh mục |
| Phân tích | "FPT lúc này thế nào?", "có nên mua NVDA?"; `#analyze-ticker` | Phân tích viên viết, Quản trị rủi ro phản biện, Quản lý danh mục quyết |
| Lệnh giấy | Tín hiệu mua/bán độ tin cậy ≥ 3 sau tranh biện; `#paper-trade`. Quy mô tính từ điểm cắt lỗ, rủi ro ≤ 1% vốn | Quản lý danh mục, Quản trị rủi ro kiểm |
| Theo dõi lệnh | Lịch **Theo dõi lệnh cuối ngày**; `#track-trades`. Chạm cắt lỗ hay mục tiêu thì đóng | Quản trị rủi ro |
| Đánh giá | Lịch **Đánh giá hằng tuần**; `#weekly-review`. Tín hiệu đúng/sai theo độ tin cậy, so với chỉ số, bài học | Quản trị rủi ro đánh giá, Phân tích viên soát |

## Agent
- **Quản lý danh mục** (mặc định): nói chuyện với bạn, điều phối, quyết định tín hiệu và lệnh giấy.
- **Phân tích viên**: tra web, phân tích bốn góc, viết báo cáo; phe tăng khi tranh biện.
- **Quản trị rủi ro**: phe giảm khi tranh biện, kiểm quy mô lệnh, theo dõi và đánh giá. Nên gắn kết nối AI khác hãng với Phân tích viên.

## Dữ liệu
- `watchlist`: mã, thị trường, ngành, vì sao, mức giá chú ý.
- `signals`: tín hiệu, độ tin cậy, giá, vùng mua, cắt lỗ, mục tiêu, ngày đánh giá, kết quả.
- `paper_trades`: lệnh giả lập, giá vào/ra có nguồn, lãi/lỗ, bội số R.
- `lessons`: kinh nghiệm rút ra, số lần gặp lại; gặp 3 lần thành quy tắc (bạn duyệt).

Cách phân tích ở skill `analysis`; tranh biện ở `debate`; quy mô và giới hạn ở `risk`; đánh giá và bài học ở `review`.

## Thư mục
- `docs/profile.md`: hồ sơ nhà đầu tư (thị trường, vốn giấy, kiểu đầu tư, mức rủi ro).
- `docs/risk-rules.md`: quy tắc rủi ro riêng, thắng mặc định.
- `analysis/`: mỗi lần phân tích một file.
- `journal/`: bản tin sáng, đánh giá tuần.
- `sources/data-sources.md`: nguồn dữ liệu theo thị trường; `sources/references.md`: các dự án tham khảo.

## Bắt đầu
1. Mở chat với Quản lý danh mục, nói thị trường, vốn giấy, kiểu đầu tư, mức rủi ro.
2. Đưa vài mã muốn theo dõi, rồi hỏi phân tích một mã.
3. Bật lịch **Bản tin sáng**, **Theo dõi lệnh cuối ngày**, **Đánh giá hằng tuần** ở trang Tự động hóa của project; ba lịch tắt sẵn.
4. Sau vài tuần, xem đánh giá: tín hiệu độ tin cậy cao có đúng hơn không, trước khi tin vào nó.
