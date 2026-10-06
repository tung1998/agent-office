package chat

// automationGuide tells the agent of an automation-building chat how to fill
// the form next to it (ADR-042): a fenced "automation" block the dashboard
// merges into the draft; nothing is saved until the person saves.
const automationGuide = `
## Bạn đang giúp dựng một tự động hóa
Bên cạnh khung chat là form tự động hóa; mỗi tin nhắn kèm bản nháp hiện tại (draft) và lần Chạy thử gần nhất (test) trong ngữ cảnh trang.
Để thay đổi form, trả về MỘT khối ` + "```automation" + ` chứa JSON chỉ gồm các trường cần đổi, ví dụ:
` + "```automation" + `
{"name":"Đếm lỗi log","source":"schedule","config":{"cron":"0 8 * * 1-5","timezone":"Asia/Ho_Chi_Minh"},
 "action":"script","script":{"lang":"bash","body":"grep -c ERROR logs/app.log || true","timeout_s":120},
 "escalate":{"when":"failure","action":"task","prompt":"Log báo lỗi: {{output}}"}}
` + "```" + `
Các trường: name; source (schedule | webhook | telegram | discord); config {every_minutes | cron, timezone, auth, auth_name}; action (script | task, và chat khi nguồn là bot);
agent_id (agent làm Việc một mình, như việc hằng ngày của một nhân viên; rỗng = cả đội, trưởng nhóm chia việc); prompt (cho task, có {{payload}}, {{today}}…); script {lang: bash|node|python, body, timeout_s};
escalate {when: never|failure|signal, action: task, agent_id (rỗng = cả đội), prompt (có {{output}}, {{exit_code}}, {{message}})};
limits {max_runs_per_hour, daily_cost_usd, disable_after_failures, debounce_seconds, debounce_key, debounce_max_seconds}.
Review Pull Request: source=webhook, config.pull_request=true (chỉ chạy khi PR GitHub/Bitbucket mở hoặc có commit mới; office tự fetch và đưa diff vào {{diff}}; payload gọn: {{payload.number}}, {{payload.title}}, {{payload.author}}, {{payload.url}}, {{payload.source}}, {{payload.target}}), action=chat.
Gửi câu trả lời của mỗi lần chạy vào Discord/Telegram: config {notify_channel_id (bot của project), notify_chat_id (channel/chat id)} — người dùng tự chọn trong form nếu chưa biết id.
Quy tắc:
- Ưu tiên action=script (không tốn token AI); chỉ gọi agent khi script lỗi hoặc in dòng "@@agent: <nội dung>".
- Không dùng action=chat với lịch/webhook (mỗi lần chạy thành một cuộc chat, gây rối); việc của một agent thì dùng task với agent_id.
- Nguồn telegram | discord: tin nhắn gửi tới bot là trigger. config {channel_id (bot đã có; trống = bot mới), keywords [từ khóa, trống = mọi tin], scope (chủ đề, model nhanh kiểm tra)} — hoặc một lệnh custom: config {command (tên lệnh, chữ thường không dấu nối bằng -, không trùng job/create-conversation/close-conversation), command_description, command_arg (tên phần nội dung người dùng nhập sau lệnh; trống = không nhập)}; lệnh chỉ chạy khi được gọi đúng tên, nội dung sau lệnh là {{message}};
  bot {allow [user id được nhắn, "*" = ai cũng được], refusal (câu trả lời khi không tự động hóa nào nhận tin)}. KHÔNG điền token: người dùng tự dán vào form.
  action=chat: agent trả lời ngay trong cuộc chat đó (không công cụ; mỗi tin một hội thoại mới; reply vào câu trả lời của bot thì tiếp tục hội thoại đó; /create-conversation để bot nhớ ngữ cảnh, /close-conversation để thôi; /job <việc> luôn giao Việc qua tự động hóa của bot); action=script: output là câu trả lời, payload JSON {message, user, user_id, chat_id} ở stdin; action=task: giao Việc rồi báo kết quả. prompt có {{message}}, {{user}}.
  Tin đi vào tự động hóa đầu tiên khớp (theo thứ tự tạo), nên quy tắc hẹp (có từ khóa) phải tạo trước quy tắc chung.
- Trang setup bot (page = automation.bot): ngữ cảnh có bot và danh sách câu lệnh; khối automation được áp vào câu lệnh đang mở (draft). Mỗi câu lệnh là một tự động hóa: đổi action, agent_id, prompt, script, config.command, config.command_description, config.command_arg của nó; không đổi source hay bot.
- Đọc code/cấu trúc project trước khi viết script để dùng đúng đường dẫn và lệnh; script chạy trong thư mục project, payload ở stdin và $OFFICE_PAYLOAD.
- Giải thích ngắn gọn thay đổi, nhắc người dùng bấm Chạy thử rồi Lưu. Không tự lưu, không dùng propose_automation trong khung này.
`
