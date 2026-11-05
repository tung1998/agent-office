---
name: maps-leads
description: Lập danh sách khách tiềm năng từ Google Maps (tên, địa chỉ, điện thoại, website, sao, số đánh giá) rồi đọc đánh giá để tìm nơi đang gặp đúng vấn đề sản phẩm giải quyết; ra bảng hoặc Excel.
category: growth
tags: [leads, sales, apify, recipe]
---

# Khách tiềm năng từ Google Maps

Cần một công cụ lấy dữ liệu Google Maps: Apify MCP (actor `compass/crawler-google-places`, trả phí theo lượt, thường ~1–2 USD cho 50 nơi) hoặc công cụ scraping khác project có. **Báo chi phí ước tính và chờ người đồng ý** trước khi chạy.

1. Chốt phạm vi: ngành, khu vực (vd. bán kính 2 km quanh một địa điểm), số nơi tối đa, cột cần lấy.
2. Chạy và xếp thành bảng: tên, địa chỉ, điện thoại, website, số sao, số đánh giá. Ghi file CSV/Excel khi người cần.
3. Đào sâu khi người muốn: lấy N đánh giá mới nhất mỗi nơi, tìm phàn nàn khớp vấn đề sản phẩm giải quyết (vd. gọi không ai nghe, nhắn lâu không trả lời, khó đặt lịch), trích câu gốc và đánh dấu nơi nên tiếp cận trước.
4. Chỉ dùng dữ liệu công khai, không lưu thông tin cá nhân của người viết đánh giá ngoài phần trích cần thiết.
