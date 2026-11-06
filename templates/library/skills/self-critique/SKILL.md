---
name: self-critique
description: Trước khi gửi báo cáo/bài viết, tự chấm bản nháp theo rubric ngắn và sửa một lần, không cần dựng workflow review-verify riêng.
---

# Self-critique

Dùng khi một agent đơn viết xong báo cáo/bài viết và cần tự kiểm trước khi gửi (khác với workflow review→verify nhiều subagent dùng cho review code/diff: đây là một bước agent tự làm trong lượt của chính nó).

1. Viết nháp đầy đủ trước, không tự sửa giữa chừng.
2. Tự chấm nháp theo đúng 5 tiêu chí sau, mỗi tiêu chí trả lời Đạt/Không đạt kèm một câu dẫn chứng từ chính nháp:
   - **Đúng câu hỏi**: trả lời đúng điều được hỏi, không lạc đề hoặc chỉ trả lời một phần.
   - **Có bằng chứng**: mỗi nhận định quan trọng có dẫn chứng (số liệu, trích dẫn, đường dẫn file/dòng), không phải suy đoán.
   - **Không mâu thuẫn**: các phần không tự mâu thuẫn nhau hoặc với dữ liệu đã nêu.
   - **Đủ nhưng không thừa**: không thiếu phần cần thiết để người đọc hành động được, không lan man phần không ai hỏi.
   - **Rủi ro/giới hạn được nêu rõ**: nếu có giả định, dữ liệu thiếu, hoặc rủi ro, phải nói thẳng thay vì im lặng.
3. Chỉ sửa đúng các điểm "Không đạt" một lần. Không viết lại toàn bộ, không thêm tiêu chí mới giữa chừng.
4. Gửi bản đã sửa; không lặp lại bước chấm lần hai (nếu cần nhiều vòng độc lập, đó là việc của workflow review→verify, không phải skill này).
