---
name: ad-video-pipeline
description: Làm video quảng cáo từ brief sản phẩm tới video có motion graphics; ghép product-marketing, ad-creative, video và hyperframes; mọi bước tốn tiền chờ người duyệt giá.
category: marketing
tags: [ads, video, recipe]
---

# Video quảng cáo từ ý tưởng tới thành phẩm

Mỗi bước dưới đây đọc skill thư viện bằng `find_skills name=<skill>` (hay cài bằng `my_skills add` khi cần cả scripts). Dừng sau mỗi bước để người chọn.

1. **Brief sản phẩm** (`product-marketing`): đọc tài liệu sản phẩm người đưa, rồi hỏi từng câu điều còn thiếu: định vị, khách là ai, vấn đề và nỗi ngại của họ, lý do họ trả tiền, giọng thương hiệu. Ghi brief ra file Markdown trong project.
2. **Góc tiếp cận** (`ad-creative`): theo nền tảng (Facebook, TikTok…) và đối tượng người nêu, đưa 3–4 góc, mỗi góc có câu mở đầu và nội dung đúng giới hạn chữ. Người chọn một góc.
3. **Kịch bản** (`ad-creative`, `video`): kịch bản 30 giây bằng ngôn ngữ của khách: lời đọc, 3–4 cảnh có mốc giây, nhân vật, bối cảnh, cảnh không có chữ.
4. **Ảnh gốc và cảnh video** (`image`, `video`, cần một MCP tạo ảnh/video như fal): viết prompt từng cảnh, **báo giá ước tính và chờ người đồng ý** trước khi tạo; giữ trong ngân sách người đặt (vd. tổng ≤ 20 USD). Có thể làm hai bản bằng hai model để so một lần, sau đó dùng model được chọn. Tự soát ảnh/cảnh với kịch bản, nêu chỗ lệch, hỏi có sửa không.
5. **Motion graphics** (`hyperframes`, `hyperframes-cli`, cần Node): ghép cảnh, giọng đọc, phụ đề, tin nhắn minh họa, logo, CTA theo màu và font lấy từ ảnh chụp trang của thương hiệu; tránh che chi tiết chính và vùng giao diện của nền tảng quảng cáo. Bỏ lệnh `npx hyperframes events` (gửi telemetry). Render, tự kiểm lỗi (tương phản chữ, tràn khung) rồi sửa.

Không có MCP tạo ảnh/video: dừng ở bước 3 và giao kịch bản cùng prompt từng cảnh.
