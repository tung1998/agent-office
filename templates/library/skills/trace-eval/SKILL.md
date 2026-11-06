---
name: trace-eval
description: Trước khi merge một bản sửa prompt agent/skill trong templates/library/, chạy lại vài transcript thật với bản mới và so với bản cũ theo rubric ngắn, để biết sửa có làm tệ đi không thay vì chỉ đoán.
---

# Trace-based agent eval

Dùng khi vừa sửa (hoặc sắp sửa) prompt của một agent/skill trong `templates/library/` và muốn biết bản mới có hồi quy so với bản cũ không. Khác với `self-critique` (một agent tự chấm nháp của chính nó trong một lượt): ở đây so sánh hai PHIÊN BẢN PROMPT trên cùng input thật, đã chạy trước đó.

1. **Chọn transcript**: lấy 3-5 lượt thật đã từng chạy với agent/skill đó — qua `burn_list` (piece đã closed liên quan), `search_history` + `read_link` (chat cũ), hoặc file transcript đã lưu. Ưu tiên lượt có input đa dạng (việc đơn giản, việc có ràng buộc, việc dễ bị bỏ sót bước) hơn là nhiều lượt giống nhau. Không có transcript thật nào thì dừng, nói rõ thiếu dữ liệu — không bịa input.
2. **Lấy bản cũ của prompt**: `git show <commit trước>:<path file agent/skill>` để có đúng bản đã sinh ra các transcript đó.
3. **Chạy lại từng input** với bản mới (prompt hiện tại trong working tree), giữ nguyên input/ngữ cảnh của transcript gốc.
4. **So theo rubric ngắn**, mỗi transcript, mỗi tiêu chí trả lời Đạt/Không đạt kèm trích dẫn cụ thể (đoạn nào khác):
   - **Đúng việc được giao**: bản mới vẫn giải quyết đúng yêu cầu như bản cũ.
   - **Không bỏ bước đã có**: mọi bước/luồng bản cũ từng làm đúng, bản mới vẫn làm (không bị quên khi prompt bị rút gọn).
   - **Không phá quy tắc/giới hạn cũ**: các ràng buộc, giới hạn, hoặc cách xử lý ca đặc biệt bản cũ tôn trọng, bản mới vẫn tôn trọng.
5. **Kết luận**: liệt kê transcript nào tốt lên, transcript nào hồi quy (có Không đạt), transcript nào không đổi. Hồi quy thì trích đúng đoạn khác biệt gây ra, không chỉ nói chung chung "có vẻ tệ hơn".
6. Không chạy hàng chục transcript hay dựng workflow review-verify cho việc này — 3-5 transcript, một lượt so sánh là đủ; nhiều vòng độc lập hơn thì đó là việc của workflow, không phải skill này.
