---
name: community-pulse
description: Xem cộng đồng (Reddit, X, YouTube, TikTok, HN…) nói gì về một chủ đề trong 30 ngày qua bằng last30days, rồi lọc theo bối cảnh của project; dùng cho nghiên cứu thị trường, sản phẩm, chủ đề nội dung.
category: research
tags: [research, market, recipe]
---

# Cộng đồng đang nói gì

Dùng skill `last30days` (cần scripts và `python3`; key như `SCRAPECREATORS_API_KEY` là tuỳ chọn). Kiểm tra an toàn của office chặn nó vì vài file gợi ý cài Grok CLI bằng `curl … | bash`: agent không đọc hay cài được, người tự xem và quyết ở trang Thư viện. Chưa có thì làm tay: tìm theo `site:reddit.com`, YouTube, HN… giới hạn 30 ngày, rồi tổng hợp như dưới.

1. Viết chủ đề thành câu hỏi cụ thể, vd. "chủ doanh nghiệp nhỏ nói gì về AI chatbot và voice agent: họ sợ gì, khách họ ghét gì, điều gì khiến họ trả tiền".
2. Tìm nguồn bằng tiếng Anh khi chủ đề đi trước ở thị trường nước ngoài (AI, SaaS); trả lời bằng ngôn ngữ của người.
3. Đọc bối cảnh project (sản phẩm, khách, kênh) trước khi tổng hợp: chỉ giữ ý liên quan tới việc của người.
4. Kết quả: tóm tắt một câu, rồi các nhóm ý (nỗi sợ, điều ghét, lý do trả tiền, câu hỏi hay gặp…), mỗi ý có trích dẫn, link nguồn và mức quan tâm (upvote, lượt xem). Tách ý cộng đồng với ý của bạn.
5. Biến thể: "chủ đề đang lên ở nước ngoài mà trong nước chưa ai làm" → so độ bàn tán với độ phủ nội dung trong nước, xếp theo mức liên quan tới kênh của người.
