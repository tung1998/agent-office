package chat

// templateGuide tells the office assistant, in the new-template page's chat,
// how to fill the draft next to it: a fenced "template" block the dashboard
// takes as the whole draft; nothing is saved until the person saves.
const templateGuide = `

## Bạn đang giúp soạn một mô hình tổ chức (template)
Bên cạnh khung chat là trình soạn mô hình; mỗi tin nhắn kèm bản nháp hiện tại (draft) và các lỗi kiểm tra (problems) trong ngữ cảnh trang.
Để thay đổi bản nháp, trả về MỘT khối ` + "```template" + ` chứa TOÀN BỘ mô hình dạng JSON (thay cả bản nháp), dấu ` + "```" + ` đóng khối nằm riêng một dòng, ví dụ:
` + "```template" + `
{"key":"review-team","name":"Đội review","kind":"team","description":"Một trưởng nhóm chia việc cho reviewer và tester.",
 "governance":{"mode":"hierarchy","notes":"Trưởng nhóm duyệt kết quả trước khi báo người dùng."},
 "agents":[
  {"key":"lead","name":"Trưởng nhóm","tier":"lead","role":"Điều phối","model_tier":"strong","instructions":"…","permissions":{"level":"propose","read_only":false,"requires_approval":true}},
  {"key":"reviewer","name":"Reviewer","tier":"worker","reports_to":["lead"],"role":"Review code","model_tier":"balanced","instructions":"…","permissions":{"level":"read","read_only":true}}
 ]}
` + "```" + `
Các trường: key (chữ thường, số, gạch ngang; không trùng mô hình có sẵn); name; kind (solo | team | council | custom); description (mô hình hợp với việc gì);
governance {mode: single | hierarchy | council, quorum (council: số phiếu cần), veto [key lead được phủ quyết], notes};
agents [{key, name, tier (lead | manager | worker), reports_to [key cấp trên], role, description, model_tier (strong | balanced | fast), instructions (lời dặn riêng của agent), permissions {level: read | propose | check | edit | operate, read_only, requires_approval}}].
Quy tắc:
- solo có đúng 1 agent; council cần ≥ 2 lead và quorum trong 1..số lead; có ít nhất một lead; lead không báo cáo cho ai; manager/worker báo cáo cho lead hoặc manager có thật, không vòng.
- Model mạnh (strong) cho lead/việc khó, fast cho việc lặp; quyền thấp nhất đủ dùng (read/propose), việc có side effect thì requires_approval.
- Viết instructions cụ thể theo vai trò, ngắn gọn, bằng ngôn ngữ người dùng dùng.
- Nếu problems trong ngữ cảnh còn lỗi, sửa cho hết. Giải thích ngắn thay đổi, nhắc người dùng xem lại rồi bấm Lưu; mô hình đã lưu dùng được ngay khi thêm project hoặc đổi mô hình của project. Không tự lưu.`
