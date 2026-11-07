# Viết MCP template cho thư viện

File này chỉ là quy ước viết, không phải nơi office đọc dữ liệu: thư mục `templates/library/mcp/` không được
embed hay seed (`templates/library/embed.go:14` chỉ embed `office/`, `agency/`, `ecc/`, `aitmpl/` và
`skills/*/SKILL.md`), nên thêm file `.json` vào đây không có tác dụng gì. `MCPTemplate` (`internal/automation/catalog.go:28`)
thật nằm ở hai chỗ: (a) template built-in khai báo cứng trong `Catalog()` (`internal/automation/catalog.go:57`),
và (b) template người dùng thêm, lưu tại `<office home>/library/mcp/<name>.json` (`internal/automation/library.go:21`).
Cả hai đều có `Title`/`Description` mà agent đọc khi chọn server để cài, rồi đọc tên và mô tả của từng tool mà
server đó trả về khi gọi thật. Theo "Designing Tools for Agents" (xem `docs/research/agent-repos.md`), giữ ba
quy ước sau cho cả hai lớp mô tả đó:

1. **Tên tool nêu rõ hành động** — động từ + đối tượng (`search_orders`, không phải `orders`); tránh tên
   chung chung như `query`, `run`, `do` khiến agent phải đoán tool làm gì.
2. **Description nêu khi nào dùng và khi nào không** — không chỉ nói tool làm gì, mà còn nói rõ ranh giới:
   trường hợp nào nên dùng tool khác thay vì tool này. Ví dụ: `fetch` (trong `Catalog()`,
   `internal/automation/catalog.go:78`) hiện chỉ ghi "Tải trang web và chuyển sang Markdown (cần uv)" — không
   nói rõ là *không* dùng được cho trang cần đăng nhập hoặc render bằng JS (khi đó nên dùng `playwright`/
   `chrome-devtools` thay vì `fetch`).
3. **Lỗi trả về phải là thứ agent sửa được** — không trả `"failed"` hay `"error"` trơn. Nêu rõ cái gì sai và
   agent cần làm gì tiếp theo (thiếu input nào, giá trị nào không hợp lệ). Ví dụ đã đúng chuẩn này:
   `Render()` (`internal/automation/catalog.go:108`) trả `"thiếu %s"` kèm tên input còn thiếu, chứ không chỉ
   `"render failed"`.

Khi thêm template mới: điền `Description` theo quy ước (2), đặt tên input (`Input.Label`,
`internal/automation/catalog.go:18`) đủ rõ để lỗi ở quy ước (3) có nghĩa, và nếu server có nhiều tool dễ nhầm
lẫn nhau, ghi chú ranh giới đó ngay trong `Description` hoặc `Homepage` thay vì để agent tự thử sai.
