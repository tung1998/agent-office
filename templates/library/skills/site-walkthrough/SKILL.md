---
name: site-walkthrough
description: Đi một vòng website như khách thật bằng trình duyệt (Playwright), khổ điện thoại, chụp màn hình chỗ đáng chú ý, rồi báo chỗ khó dùng và đề xuất có ảnh khoanh.
category: design
tags: [ux, audit, playwright, recipe]
---

# Đi một vòng website như khách hàng

Cần một trình duyệt agent điều khiển được (Playwright MCP hay `playwright` CLI), chạy profile riêng, không dùng tài khoản đang đăng nhập của người.

1. Mở URL ở khổ điện thoại (vd. 390×844) trừ khi người nói khác.
2. Nhập vai khách mà người nêu (vd. chủ phòng khám vừa thấy quảng cáo, có 5 phút để quyết định dùng thử). Không có vai thì hỏi một câu.
3. Cuộn hết trang chủ, mở menu, bấm các nút và link sang trang khác. **Không điền form, không nhắn vào khung chat, không mua hay đăng nhập** trừ khi người cho phép rõ.
4. Chụp màn hình mỗi chỗ đáng chú ý, lưu vào thư mục tạm của project; báo tiến độ ngắn sau mỗi trang.
5. Báo cáo:
   - Điều khách cần biết mà trang chưa trả lời.
   - Chỗ khó dùng trên điện thoại: chữ nhỏ, nút khó bấm, nút chính khuất màn hình đầu, nút chat che chữ, phải cuộn quá lâu, link hỏng.
   - 5 đề xuất ưu tiên, mỗi cái kèm ảnh đã khoanh vùng và lý do.
6. Ghi lại các bước đã đi dưới dạng script Playwright khi người muốn chạy lại về sau.
