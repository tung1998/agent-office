// Package officetools gives agents read-only access to what office knows
// about a project at run time: processes (build/dev/test), docker compose
// services, health checks and their incidents. The same tools are served to
// Claude Code over MCP (internal/mcpserver) and to API agents directly.
package officetools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/perm"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/storage"
)

// Tool is one callable tool.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Toolbox runs the tools for one project at a time.
type Toolbox struct {
	store   storage.Store
	ops     *ops.Manager
	actions *actions.Service
	// delegate hands a task to another agent of the chat (ADR-044), set by the chat engine
	delegate func(ctx context.Context, sc Scope, agent, task string) (string, error)
	sendFile func(ctx context.Context, sc Scope, path, caption string) (string, error)
	burn     func(ctx context.Context, sc Scope, name string, in BurnInput) (string, error)
	// config reads settings for describe/list/get (the API's registry, ADR-045)
	config ConfigReader
	// assistant is the office assistant's own project (hidden from the list)
	assistant func(ctx context.Context) string
}

// SetOffice tells the tools which project is the office assistant's.
func (t *Toolbox) SetOffice(fn func(ctx context.Context) string) { t.assistant = fn }

// ConfigReader reads settings for the generic tools.
type ConfigReader interface {
	DescribeConfig(kind string) (string, error)
	ListConfig(ctx context.Context, projectID, kind string) (string, error)
	GetConfig(ctx context.Context, projectID, kind, id string) (string, error)
}

// SetConfig turns on describe, list, get and propose_change.
func (t *Toolbox) SetConfig(c ConfigReader) { t.config = c }

// SetDelegate turns on the delegate tool (the chat engine runs hand-offs).
func (t *Toolbox) SetDelegate(fn func(ctx context.Context, sc Scope, agent, task string) (string, error)) {
	t.delegate = fn
}

// BurnInput is what the burn_* tools take.
type BurnInput struct{ Title, Kind, Detail, Item, Summary, Reason string }

// SetBurn turns on the burn_* tools (a Burn's conversation only).
func (t *Toolbox) SetBurn(fn func(ctx context.Context, sc Scope, name string, in BurnInput) (string, error)) {
	t.burn = fn
}

// SetSendFile turns on the send_file tool (a bot's chat posts the file, ADR-083).
func (t *Toolbox) SetSendFile(fn func(ctx context.Context, sc Scope, path, caption string) (string, error)) {
	t.sendFile = fn
}

// Scope is who calls a tool: the project, and the conversation/task and run
// that proposals are attached to.
type Scope = actions.Scope

// New builds a Toolbox; ops may be nil (processes/containers unavailable),
// acts may be nil (no propose_action).
func New(store storage.Store, o *ops.Manager, acts *actions.Service) *Toolbox {
	return &Toolbox{store: store, ops: o, actions: acts}
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var linesProp = map[string]any{"type": "integer", "description": "Số dòng log cuối (mặc định 200, tối đa 1000)"}

// Tools lists the tools (names are stable: agents and the UI refer to them).
func (t *Toolbox) Tools() []Tool {
	list := []Tool{
		{Name: "ops_overview", Description: "Tổng quan vận hành của project: các tiến trình (dev, build, test…) và trạng thái/mã thoát/cổng, các service docker compose, các giám sát (Up/Down) và sự cố gần đây. Gọi đầu tiên khi được hỏi về lỗi build, lỗi chạy, deploy hay giám sát.",
			Schema: obj(map[string]any{})},
		{Name: "process_logs", Description: "Đọc log gần nhất của một tiến trình office chạy cho project (ví dụ dev, build, test), kèm lệnh, trạng thái và mã thoát.",
			Schema: obj(map[string]any{"name": map[string]any{"type": "string", "description": "Tên tiến trình, xem ops_overview"}, "lines": linesProp}, "name")},
		{Name: "container_logs", Description: "Đọc log gần nhất của một service docker compose của project, kèm trạng thái container.",
			Schema: obj(map[string]any{"service": map[string]any{"type": "string"}, "lines": linesProp}, "service")},
		{Name: "monitor_detail", Description: "Chi tiết một giám sát: cấu hình, các lần kiểm tra gần đây, sự kiện Up/Down và phân tích AI trước đó.",
			Schema: obj(map[string]any{"name": map[string]any{"type": "string", "description": "Tên giám sát, xem ops_overview"}}, "name")},
	}
	list = append(list,
		Tool{Name: "git_status", Description: "Trạng thái git của project: nhánh, số commit chưa push, các file đang thay đổi.", Schema: obj(map[string]any{})},
		Tool{Name: "git_diff", Description: "Diff của các thay đổi chưa commit (so với HEAD), có thể giới hạn theo files.",
			Schema: obj(map[string]any{"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}})},
		Tool{Name: "git_log", Description: "Các commit gần nhất.", Schema: obj(map[string]any{"lines": map[string]any{"type": "integer"}})},
	)
	if t.actions != nil {
		list = append(list, Tool{Name: "run_command", Description: "Chạy một lệnh trong thư mục project (không qua shell: không dùng | ; & > $, mỗi lần một lệnh), ví dụ test, typecheck, lint, build, git log. " +
			"Lệnh nằm trong danh sách được phép của bạn chạy ngay và trả về output; lệnh khác thành đề xuất chờ người dùng duyệt.",
			Schema: obj(map[string]any{
				"command": map[string]any{"type": "string", "description": "Dòng lệnh, ví dụ: go test ./internal/..."},
				"reason":  map[string]any{"type": "string", "description": "Vì sao cần chạy"},
			}, "command", "reason")})
		list = append(list, Tool{Name: "propose_action", Description: "Đề xuất một thao tác để người dùng duyệt (tự chạy nếu gói quyền cho phép): " +
			"run_process / restart_process / stop_process (target = tên tiến trình), start_container / restart_container / stop_container (target = service docker compose), " +
			"git_commit (message = commit message theo quy ước repo, files = danh sách file; bỏ trống files = mọi thay đổi), git_branch (branch = tên nhánh mới), git_push (đẩy nhánh hiện tại, luôn cần người duyệt). " +
			"Dùng sau khi đề xuất sửa code để chạy lại build/test kiểm chứng, khởi động lại dịch vụ bị treo, hoặc khi người dùng nhờ commit/push. Luôn nêu lý do.",
			Schema: obj(map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"run_process", "restart_process", "stop_process", "start_container", "restart_container", "stop_container", "git_commit", "git_branch", "git_push"}},
				"target":  map[string]any{"type": "string"},
				"reason":  map[string]any{"type": "string", "description": "Vì sao cần thao tác này"},
				"message": map[string]any{"type": "string", "description": "git_commit: commit message"},
				"files":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "git_commit: file cần commit"},
				"branch":  map[string]any{"type": "string", "description": "git_branch: tên nhánh"},
			}, "action", "reason")})
		list = append(list, Tool{Name: "send_to_chat", Description: "Gửi một tin nhắn vào cuộc chat KHÁC của office, dưới tên người dùng đang chat với bạn (như họ tự gõ), " +
			"để agent của chat đó làm tiếp. Dùng khi người dùng nhờ chuyển việc, nhắn hay hỏi sang chat khác. Tin đi kèm ghi chú gửi từ chat này. " +
			"Tùy quyền, gửi ngay hoặc thành đề xuất chờ người dùng duyệt. Tìm chat bằng search_history hoặc dùng link người dùng dán.",
			Schema: obj(map[string]any{
				"chat":   map[string]any{"type": "string", "description": "Cuộc chat đích: link dashboard (…?tab=chat&c=…) hoặc mã cnv_…"},
				"text":   map[string]any{"type": "string", "description": "Nội dung tin nhắn, đủ rõ để agent bên đó làm mà không phải hỏi lại"},
				"reason": map[string]any{"type": "string", "description": "Vì sao cần gửi"},
			}, "chat", "text", "reason")})
		list = append(list, Tool{Name: "remember", Description: "Ghi một điều đáng nhớ lâu dài vào sổ ghi nhớ của bạn ở project này (có ở mọi cuộc chat sau): " +
			"quy ước của repo, quyết định đã chốt, điều người dùng muốn hay không muốn, lỗi đã gặp và cách sửa. Một ý ngắn, rõ, mỗi lần một điều; " +
			"không ghi việc chỉ của lần này, không ghi bí mật. Tùy cài đặt project, ghi ngay hoặc chờ người dùng duyệt.",
			Schema: obj(map[string]any{
				"note":   map[string]any{"type": "string", "description": "Điều cần nhớ, ví dụ: Repo dùng pnpm; chạy pnpm test trước khi báo xong"},
				"reason": map[string]any{"type": "string", "description": "Vì sao đáng nhớ"},
			}, "note")})
		str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
		list = append(list, Tool{Name: "propose_automation", Description: "Đề xuất một tự động hóa cho project (luôn chờ người dùng duyệt). " +
			"Ưu tiên action=script (bash/node/python, chạy trong thư mục project, KHÔNG tốn token AI): script nhận payload qua stdin và $OFFICE_PAYLOAD, in kết quả ra stdout; " +
			"thoát khác 0 khi có lỗi; in dòng '@@agent: <nội dung>' khi cần agent xem. escalate.when: never | failure (mặc định, khi script lỗi) | signal (khi có dòng @@agent). " +
			"Chỉ dùng action=chat/task khi mỗi lần chạy thật sự cần AI. Lịch: every_minutes hoặc cron 5 trường kèm timezone (IANA). Có automation_id thì là sửa tự động hóa đó.",
			Schema: obj(map[string]any{
				"automation_id": str("Sửa tự động hóa này (bỏ trống = tạo mới)"),
				"name":          str("Tên ngắn"),
				"source":        map[string]any{"type": "string", "enum": []string{"schedule", "webhook"}},
				"every_minutes": map[string]any{"type": "integer"},
				"cron":          str("Ví dụ 0 8 * * 1-5"),
				"timezone":      str("Ví dụ Asia/Ho_Chi_Minh"),
				"action":        map[string]any{"type": "string", "enum": []string{"script", "chat", "task"}},
				"agent_id":      str("chat: agent trả lời (bỏ trống = trưởng nhóm)"),
				"prompt":        str("chat/task: nội dung gửi agent, có {{payload}}, {{today}}…"),
				"script": obj(map[string]any{
					"lang":      map[string]any{"type": "string", "enum": []string{"bash", "node", "python"}},
					"body":      str("Mã nguồn script"),
					"timeout_s": map[string]any{"type": "integer", "description": "Mặc định 300, tối đa 3600"},
				}),
				"escalate": obj(map[string]any{
					"when":     map[string]any{"type": "string", "enum": []string{"never", "failure", "signal"}},
					"action":   map[string]any{"type": "string", "enum": []string{"chat", "task"}},
					"agent_id": str("Agent xử lý (bỏ trống = trưởng nhóm)"),
					"prompt":   str("Nội dung gửi agent, có {{output}}, {{exit_code}}, {{message}}"),
				}),
				"ends_at": str("schedule: giờ tự dừng (RFC3339, ví dụ 2026-10-12T00:00:00+07:00); bỏ trống = chạy tới khi tắt"),
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "chat: tag gắn vào chat của mỗi lần chạy (tối đa 10; bỏ trống = giữ tag cũ)"},
				"reason":  str("Vì sao cần tự động hóa này"),
			}, "name", "source", "action", "reason")})
	}
	if t.config != nil && t.actions != nil {
		kind := map[string]any{"type": "string", "description": "Loại cài đặt, xem describe"}
		list = append(list,
			Tool{Name: "describe", Description: "Các loại cài đặt đổi được (automation, agent, monitor, process, policy, project, usage_settings, provider); có resource thì liệt kê trường sửa được.",
				Schema: obj(map[string]any{"resource": kind})},
			Tool{Name: "list", Description: "Danh sách cài đặt của một loại trong project (id, tên, trạng thái).", Schema: obj(map[string]any{"resource": kind}, "resource")},
			Tool{Name: "get", Description: "Một cài đặt đầy đủ (bí mật đã che). policy, project, usage_settings không cần id.",
				Schema: obj(map[string]any{"resource": kind, "id": map[string]any{"type": "string"}}, "resource")},
			Tool{Name: "propose_change", Description: "ĐỀ XUẤT đổi một cài đặt: người dùng duyệt trên thẻ rồi office mới đổi, như khi họ sửa trên dashboard. " +
				"patch là object JSON chỉ gồm các trường cần đổi (xem describe/get). Không đưa API key: người dùng tự dán trên thẻ.",
				Schema: obj(map[string]any{
					"resource": kind,
					"op":       map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}},
					"id":       map[string]any{"type": "string", "description": "Cài đặt cần sửa/xóa (trống khi tạo mới, và với policy/project/usage_settings)"},
					"patch":    map[string]any{"type": "object"},
					"reason":   map[string]any{"type": "string", "description": "Vì sao cần đổi"},
				}, "resource", "op", "reason")},
		)
	}
	list = append(list, Tool{Name: "search_history", Description: "Tìm trong lịch sử các cuộc chat (web, Discord, Telegram, tự động hóa) theo từ khóa, không phân biệt dấu. " +
		"Dùng khi người dùng nhắc tới việc cũ (\"gần đây mình có nhờ bạn…\", \"lần trước bạn sửa…\"): tìm trước, đừng đoán. Kết quả có link; đọc kỹ một cuộc chat bằng read_link.",
		Schema: obj(map[string]any{
			"query":  map[string]any{"type": "string", "description": "Từ khóa, vài chữ là đủ"},
			"days":   map[string]any{"type": "integer", "description": "Trong bao nhiêu ngày gần đây (mặc định 30, tối đa 365)"},
			"author": map[string]any{"type": "string", "description": "Chỉ tin của người/agent này (không bắt buộc)"},
		}, "query")})
	list = append(list, Tool{Name: "read_link", Description: "Đọc nội dung một liên kết của office mà người dùng dán vào: một cuộc chat (…?tab=chat&c=…), một tin nhắn (&m=…) hoặc một Việc (…?tab=tasks&task=…) của project này.",
		Schema: obj(map[string]any{"url": map[string]any{"type": "string", "description": "Liên kết dashboard của office"}}, "url")})
	if t.burn != nil {
		item := map[string]any{"type": "string", "description": "Mã việc (bit_…)"}
		list = append(list,
			Tool{Name: "burn_add", Description: "Burn: ghi một việc tìm thấy (không ghi trùng).", Schema: obj(map[string]any{
				"title":  map[string]any{"type": "string", "description": "Tiêu đề ngắn"},
				"kind":   map[string]any{"type": "string", "enum": []string{"unfinished", "upgrade", "bug"}},
				"detail": map[string]any{"type": "string", "description": "Đủ để làm: ở đâu, vì sao, làm thế nào là xong"},
			}, "title", "kind")},
			Tool{Name: "burn_pick", Description: "Burn: chọn việc làm tiếp theo.", Schema: obj(map[string]any{"item": item}, "item")},
			Tool{Name: "burn_skip", Description: "Burn: bỏ qua một việc không đáng làm.", Schema: obj(map[string]any{"item": item, "reason": map[string]any{"type": "string"}}, "item", "reason")},
			Tool{Name: "burn_done", Description: "Burn: báo xong việc đang làm.", Schema: obj(map[string]any{"item": item, "summary": map[string]any{"type": "string", "description": "Đã làm gì, kiểm chứng ra sao"}}, "item", "summary")},
			Tool{Name: "burn_fail", Description: "Burn: báo không làm được việc đang làm.", Schema: obj(map[string]any{"item": item, "reason": map[string]any{"type": "string"}}, "item", "reason")},
		)
	}
	if t.sendFile != nil {
		list = append(list, Tool{Name: "send_file", Description: "Gửi một file (ảnh png/jpg/gif/webp hiện dạng ảnh; file khác dạng tài liệu) vào cuộc chat Discord/Telegram đang nói chuyện, ngay lúc gọi. " +
			"Dùng khi người dùng cần xem ảnh chụp màn hình, biểu đồ, file log… File phải nằm trong thư mục làm việc của project (chép vào đó trước nếu cần).",
			Schema: obj(map[string]any{
				"path":    map[string]any{"type": "string", "description": "Đường dẫn file (tương đối theo thư mục làm việc, hoặc tuyệt đối trong đó)"},
				"caption": map[string]any{"type": "string", "description": "Chú thích ngắn kèm file (không bắt buộc)"},
			}, "path")})
	}
	if t.delegate != nil {
		list = append(list, Tool{Name: "delegate", Description: "Giao một phần việc cho agent khác trong cuộc chat, như subagent: agent đó làm ở nền, bạn trả lời người dùng ngay; " +
			"khi nó xong, kết quả hiện trong cuộc chat và bạn được gọi lại để báo cho người dùng. Chỉ dùng khi thật sự cần (việc cần quyền hay chuyên môn bạn không có). " +
			"Viết @Tên trong câu trả lời chỉ là nhắc tên, KHÔNG giao việc.",
			Schema: obj(map[string]any{
				"agent": map[string]any{"type": "string", "description": "Tên agent của project"},
				"task":  map[string]any{"type": "string", "description": "Việc cần làm, đủ rõ để làm mà không phải hỏi lại"},
			}, "agent", "task")})
	}
	return list
}

// ToolsFor lists the tools an agent at level may use (proposals need
// "propose"; below it run_command runs only its safe commands).
func (t *Toolbox) ToolsFor(sc Scope) []Tool {
	var out []Tool
	for _, x := range t.Tools() {
		if (x.Name == "propose_action" || x.Name == "propose_automation" || x.Name == "send_to_chat") && !perm.AtLeast(sc.Level, perm.Propose) {
			continue
		}
		if x.Name == "send_to_chat" && sc.ConversationID == "" {
			continue
		}
		if strings.HasPrefix(x.Name, "burn_") && !t.burnChat(sc) {
			continue // a Burn's conversation only
		}
		if x.Name == "send_file" && !t.botChat(sc) {
			continue // only a bot's chat has somewhere to post it
		}
		if sc.Office && x.Name == "delegate" {
			continue // the assistant hands work to a project's chat instead
		}
		if sc.AnswerOnly && proposes(x.Name) {
			continue
		}
		out = append(out, x)
	}
	if sc.Office { // the office assistant (ADR-046)
		for _, x := range t.officeTools() {
			if !sc.AnswerOnly || !proposes(x.Name) {
				out = append(out, x)
			}
		}
	}
	return out
}

// proposes: a tool that proposes a change or runs something (none for an
// assistant that only answers).
func proposes(name string) bool {
	return strings.HasPrefix(name, "propose") || name == "run_automation" || name == "send_to_chat"
}

// Has reports whether name is one of the tools.
func (t *Toolbox) Has(sc Scope, name string) bool {
	for _, x := range t.ToolsFor(sc) {
		if x.Name == name {
			return true
		}
	}
	return false
}

// Call runs a tool for a project and returns text (and whether it failed).
func (t *Toolbox) Call(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool) {
	projectID := sc.ProjectID
	var in struct {
		Project  string          `json:"project"`
		Goal     string          `json:"goal"`
		Days     int             `json:"days"`
		By       string          `json:"by"`
		Status   string          `json:"status"`
		Limit    int             `json:"limit"`
		Name     string          `json:"name"`
		Service  string          `json:"service"`
		Lines    int             `json:"lines"`
		Action   string          `json:"action"`
		Target   string          `json:"target"`
		Reason   string          `json:"reason"`
		Message  string          `json:"message"`
		Files    []string        `json:"files"`
		Path     string          `json:"path"`
		Query    string          `json:"query"`
		Title    string          `json:"title"`
		Kind     string          `json:"kind"`
		Detail   string          `json:"detail"`
		Item     string          `json:"item"`
		Summary  string          `json:"summary"`
		Author   string          `json:"author"`
		Caption  string          `json:"caption"`
		Branch   string          `json:"branch"`
		Command  string          `json:"command"`
		Agent    string          `json:"agent"`
		Task     string          `json:"task"`
		URL      string          `json:"url"`
		Resource string          `json:"resource"`
		Op       string          `json:"op"`
		ID       string          `json:"id"`
		Patch    json.RawMessage `json:"patch"`
		Note     string          `json:"note"`
		Chat     string          `json:"chat"`
		Text     string          `json:"text"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không hợp lệ: " + err.Error(), true
		}
	}
	if sc.Office { // across projects: the one named, if any
		if out, isErr, ok := t.officeCall(ctx, sc, name, raw); ok {
			return out, isErr
		}
		if in.Project != "" {
			p, err := t.findProject(ctx, in.Project)
			if err != nil {
				return err.Error(), true
			}
			projectID, sc.ProjectID = p.ID, p.ID
		}
	} else if in.Project != "" && in.Project != projectID {
		return "Bạn chỉ làm việc trong project của mình", true
	}
	if in.Lines <= 0 {
		in.Lines = 200
	}
	in.Lines = min(in.Lines, 1000)
	var (
		out string
		err error
	)
	switch name {
	case "ops_overview":
		out, err = t.overview(ctx, projectID)
	case "process_logs":
		out, err = t.processLogs(ctx, projectID, in.Name, in.Lines)
	case "container_logs":
		out, err = t.containerLogs(ctx, projectID, in.Service, in.Lines)
	case "monitor_detail":
		out, err = t.monitorDetail(ctx, projectID, in.Name)
	case "git_status", "git_diff", "git_log":
		out, err = t.gitRead(ctx, projectID, sc.Dir, name, in.Files, in.Lines)
	case "run_command":
		if t.actions == nil {
			return "Office không chạy lệnh được lúc này", true
		}
		if !perm.AtLeast(sc.Level, perm.Propose) {
			// read only: checks and reads run, nothing is proposed
			args, err := perm.SplitCommand(in.Command)
			if _, safe := perm.MatchCommand(sc.Access.Safe, args); err != nil || !safe {
				return "Ở mức " + perm.Label(sc.Level) + " bạn chỉ được chạy lệnh kiểm tra an toàn: " + strings.Join(sc.Access.Safe, ", "), true
			}
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, "run_command", in.Command, in.Reason); err == nil {
			switch a.Status {
			case "done":
				out = "$ " + a.Target + "\n" + a.Detail
			case "failed":
				return "$ " + a.Target + "\nLỗi: " + a.Detail, true
			default:
				out = fmt.Sprintf("Lệnh %q không nằm trong danh sách bạn được tự chạy, đã tạo đề xuất (mã %s) chờ người dùng duyệt. Chưa chạy gì; hãy báo người dùng.", a.Target, a.ID)
			}
		}
	case "describe", "list", "get", "propose_change":
		if t.config == nil || t.actions == nil {
			return "Không có công cụ cài đặt ở đây", true
		}
		switch name {
		case "describe":
			out, err = t.config.DescribeConfig(in.Resource)
		case "list":
			out, err = t.config.ListConfig(ctx, projectID, in.Resource)
		case "get":
			out, err = t.config.GetConfig(ctx, projectID, in.Resource, in.ID)
		default:
			if !sc.Office && !perm.AtLeast(sc.Level, perm.Propose) {
				return "Bạn không có quyền đề xuất đổi cài đặt (gói hiện tại: " + perm.Label(sc.Level) + ")", true
			}
			var a storage.Action
			a, err = t.actions.Propose(ctx, sc, "config_change", "", in.Reason, storage.ActionArgs{Change: &storage.ConfigChange{Resource: in.Resource, Op: in.Op, ID: in.ID, Patch: in.Patch}})
			if err == nil {
				out = "Đã tạo thẻ duyệt: " + a.Target + ". Người dùng duyệt trên thẻ thì office mới đổi; báo họ ngắn gọn bạn đề xuất gì."
			}
		}
	case "read_link":
		out, err = t.readLink(ctx, sc, in.URL)
	case "search_history":
		out, err = t.searchHistory(ctx, sc, in.Query, in.Days, in.Author)
	case "burn_add", "burn_pick", "burn_skip", "burn_done", "burn_fail":
		if t.burn == nil || !t.burnChat(sc) {
			return "Các công cụ burn_* chỉ dùng trong hội thoại Burn", true
		}
		out, err = t.burn(ctx, sc, name, BurnInput{Title: in.Title, Kind: in.Kind, Detail: in.Detail, Item: in.Item, Summary: in.Summary, Reason: in.Reason})
	case "send_file":
		if t.sendFile == nil || !t.botChat(sc) {
			return "send_file chỉ dùng trong cuộc chat của bot Discord/Telegram", true
		}
		out, err = t.sendFile(ctx, sc, in.Path, in.Caption)
	case "delegate":
		if t.delegate == nil {
			return "Không có công cụ giao việc ở đây", true
		}
		out, err = t.delegate(ctx, sc, in.Agent, in.Task)
	case "propose_automation":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền đề xuất tự động hóa (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var id struct {
			AutomationID string `json:"automation_id"`
		}
		_ = json.Unmarshal(raw, &id)
		kind, target := "create_automation", in.Name
		if id.AutomationID != "" {
			kind, target = "update_automation", id.AutomationID
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, kind, target, in.Reason, storage.ActionArgs{Automation: raw}); err == nil {
			out = fmt.Sprintf("Đã tạo đề xuất %q (mã %s), đang chờ người dùng duyệt. Chưa có gì chạy; hãy tóm tắt cho người dùng script/lịch và nhắc họ bấm Duyệt.", a.Target, a.ID)
		}
	case "send_to_chat":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền gửi tin sang chat khác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, "send_message", in.Chat, in.Reason, storage.ActionArgs{Message: in.Text}); err == nil {
			switch a.Status {
			case "done":
				out = fmt.Sprintf("Đã gửi tin vào chat %q dưới tên người dùng; agent bên đó sẽ trả lời trong chat đó.", a.Target)
			case "failed":
				return "Gửi vào chat " + a.Target + " lỗi: " + a.Detail, true
			default:
				out = fmt.Sprintf("Đã tạo đề xuất gửi tin vào chat %q (mã %s), chờ người dùng duyệt. Chưa gửi gì; hãy báo người dùng.", a.Target, a.ID)
			}
		}
	case "remember":
		if t.actions == nil {
			return "Không ghi nhớ được ở đây", true
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, "remember", in.Note, in.Reason); err == nil {
			if a.Status == "done" {
				out = "Đã ghi vào sổ ghi nhớ: " + a.Target
			} else {
				out = "Đã đề xuất ghi nhớ (mã " + a.ID + "), chờ người dùng duyệt."
			}
		}
	case "propose_action":
		if t.actions == nil || !perm.AtLeast(sc.Level, perm.Propose) {
			return "Bạn không có quyền đề xuất thao tác (gói hiện tại: " + perm.Label(sc.Level) + ")", true
		}
		var a storage.Action
		if a, err = t.actions.Propose(ctx, sc, in.Action, in.Target, in.Reason, storage.ActionArgs{Message: in.Message, Files: in.Files, Branch: in.Branch}); err == nil {
			switch a.Status {
			case "done":
				out = fmt.Sprintf("Đã tự thực hiện %q cho %s theo quyền của bạn: %s. Dùng process_logs/container_logs để xem kết quả.", actions.Kinds[a.Kind], a.Target, a.Detail)
			case "failed":
				out = fmt.Sprintf("Đã tự thực hiện %q cho %s nhưng lỗi: %s", actions.Kinds[a.Kind], a.Target, a.Detail)
			default:
				out = fmt.Sprintf("Đã tạo đề xuất %q cho %s (mã %s), đang chờ người dùng duyệt. Bạn chưa thực hiện gì; hãy nói với người dùng là cần bấm Duyệt.", actions.Kinds[a.Kind], a.Target, a.ID)
			}
		}
	default:
		return "Công cụ không tồn tại: " + name, true
	}
	if err != nil {
		return "Lỗi: " + err.Error(), true
	}
	return out, false
}

func when(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Local().Format("02/01 15:04:05")
}

func (t *Toolbox) overview(ctx context.Context, projectID string) (string, error) {
	var b strings.Builder
	procs, err := t.store.Processes().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	b.WriteString("## Tiến trình\n")
	if len(procs) == 0 {
		b.WriteString("(chưa có)\n")
	}
	for _, p := range procs {
		line := fmt.Sprintf("- %s [%s] `%s`", p.Name, p.Kind, p.Command)
		if t.ops != nil {
			st := t.ops.State(p.ID)
			line += " — " + st.Status
			if st.ExitCode != nil {
				line += fmt.Sprintf(", mã thoát %d", *st.ExitCode)
			}
			if st.Port > 0 {
				line += fmt.Sprintf(", cổng %d", st.Port)
			}
			if st.FinishedAt != nil && st.Status != "running" {
				line += ", kết thúc " + when(st.FinishedAt)
			}
		}
		b.WriteString(line + "\n")
	}
	if t.ops != nil {
		if v, err := t.ops.Compose(ctx, projectID, "", false); err == nil && len(v.Files) > 0 {
			fmt.Fprintf(&b, "\n## Docker compose (%s)\n", v.File)
			if v.Docker.Error != "" {
				b.WriteString("Docker: " + v.Docker.Error + "\n")
			}
			for _, s := range v.Services {
				if s.Container == nil {
					fmt.Fprintf(&b, "- %s — chưa tạo container\n", s.Name)
					continue
				}
				fmt.Fprintf(&b, "- %s — %s (%s)\n", s.Name, s.Container.State, s.Container.Status)
			}
		}
	}
	mons, err := t.store.Monitors().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	b.WriteString("\n## Giám sát\n")
	if len(mons) == 0 {
		b.WriteString("(chưa có)\n")
	}
	for _, m := range mons {
		status := m.Status
		if !m.Enabled {
			status = "tạm dừng"
		}
		fmt.Fprintf(&b, "- %s [%s %s] — %s: %s (kiểm tra %s)\n", m.Name, m.Type, m.Target, status, m.LastMessage, when(m.LastCheckedAt))
	}
	evs, err := t.store.Monitors().Events(ctx, projectID, 10)
	if err == nil && len(evs) > 0 {
		names := map[string]string{}
		for _, m := range mons {
			names[m.ID] = m.Name
		}
		b.WriteString("\n## Sự kiện gần đây\n")
		for _, e := range evs {
			fmt.Fprintf(&b, "- %s %s %s: %s\n", e.At.Local().Format("02/01 15:04"), strings.ToUpper(e.Kind), names[e.MonitorID], e.Message)
		}
	}
	return b.String(), nil
}

func (t *Toolbox) processLogs(ctx context.Context, projectID, name string, lines int) (string, error) {
	if t.ops == nil {
		return "", errors.New("office không quản lý tiến trình")
	}
	procs, err := t.store.Processes().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, p := range procs {
		if strings.EqualFold(p.Name, name) || p.ID == name {
			st := t.ops.State(p.ID)
			head := fmt.Sprintf("Tiến trình %s: `%s` (thư mục %s) — %s", p.Name, p.Command, p.Cwd, st.Status)
			if st.ExitCode != nil {
				head += fmt.Sprintf(", mã thoát %d", *st.ExitCode)
			}
			tail := t.ops.Tail(p.ID, lines)
			if strings.TrimSpace(tail) == "" {
				tail = "(chưa có log trong phiên này của office)"
			}
			return head + "\n\n```\n" + tail + "```", nil
		}
	}
	return "", fmt.Errorf("không có tiến trình %q; xem ops_overview", name)
}

func (t *Toolbox) containerLogs(ctx context.Context, projectID, service string, lines int) (string, error) {
	if t.ops == nil {
		return "", errors.New("office không quản lý container")
	}
	c, err := t.ops.ServiceContainer(ctx, projectID, "", service)
	if err != nil {
		return "", err
	}
	head := "Service " + service + ": chưa tạo container"
	if c != nil {
		head = fmt.Sprintf("Service %s: %s (%s), image %s", service, c.State, c.Status, c.Image)
	}
	tail, err := t.ops.ComposeTail(ctx, projectID, "", service, lines)
	if err != nil {
		return head + "\n(không đọc được log: " + err.Error() + ")", nil
	}
	return head + "\n\n```\n" + tail + "```", nil
}

func (t *Toolbox) monitorDetail(ctx context.Context, projectID, name string) (string, error) {
	mons, err := t.store.Monitors().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, m := range mons {
		if !strings.EqualFold(m.Name, name) && m.ID != name {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Giám sát %s [%s] đích %s, mỗi %ds — %s: %s\n", m.Name, m.Type, m.Target, m.IntervalS, m.Status, m.LastMessage)
		if checks, err := t.store.Monitors().Checks(ctx, m.ID, time.Now().Add(-6*time.Hour)); err == nil && len(checks) > 0 {
			b.WriteString("\nKiểm tra gần đây:\n")
			for _, c := range checks[max(0, len(checks)-20):] {
				ok := "OK"
				if !c.OK {
					ok = "LỖI"
				}
				fmt.Fprintf(&b, "- %s %s %dms %s\n", c.At.Local().Format("15:04:05"), ok, c.LatencyMS, c.Message)
			}
		}
		if evs, err := t.store.Monitors().Events(ctx, projectID, 50); err == nil {
			n := 0
			for _, e := range evs {
				if e.MonitorID != m.ID || n >= 5 {
					continue
				}
				n++
				fmt.Fprintf(&b, "\nSự kiện %s %s: %s\n", e.At.Local().Format("02/01 15:04"), strings.ToUpper(e.Kind), e.Message)
				if e.AnalysisStatus == "done" && e.Analysis != "" {
					fmt.Fprintf(&b, "Phân tích trước đó:\n%s\n", e.Analysis)
				}
			}
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("không có giám sát %q; xem ops_overview", name)
}

// gitRead reads git in the project folder, or in the run's worktree (dir).
func (t *Toolbox) gitRead(ctx context.Context, projectID, dir, name string, files []string, lines int) (string, error) {
	p, err := t.store.Repos().Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p.Path == "" {
		return "", errors.New("project không gắn thư mục")
	}
	if dir != "" {
		p.Path = dir
	}
	switch name {
	case "git_status":
		st, err := gitops.ReadStatus(ctx, p.Path)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Nhánh %s", st.Branch)
		if st.Upstream != "" {
			fmt.Fprintf(&b, " (theo %s, hơn %d, kém %d commit)", st.Upstream, st.Ahead, st.Behind)
		}
		fmt.Fprintf(&b, "\n%d file thay đổi:\n", len(st.Changes))
		for _, c := range st.Changes {
			fmt.Fprintf(&b, "- %s %s\n", c.Status, c.Path)
		}
		return b.String(), nil
	case "git_diff":
		return gitops.Diff(ctx, p.Path, files, 60000)
	default:
		return gitops.Log(ctx, p.Path, min(max(lines, 10), 50))
	}
}

// botChat: the run is a bot's chat (Discord/Telegram), where send_file posts.
func (t *Toolbox) botChat(sc Scope) bool {
	if sc.ConversationID == "" {
		return false
	}
	c, err := t.store.Chat().GetConversation(context.Background(), sc.ConversationID)
	return err == nil && c.Purpose == "channel"
}

// searchHistory finds what was said in the chats (ADR-086): the project's,
// or every project's for the office assistant — never someone else's private
// chat with the assistant.
func (t *Toolbox) searchHistory(ctx context.Context, sc Scope, query string, days int, author string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("hãy ghi từ khóa cần tìm")
	}
	if days <= 0 {
		days = 30
	}
	f := storage.SearchFilter{Since: time.Now().UTC().AddDate(0, 0, -min(days, 365)), Author: strings.TrimSpace(author), Limit: 10}
	assistant := ""
	if t.assistant != nil {
		assistant = t.assistant(ctx)
	}
	if sc.Office {
		repos, err := t.store.Repos().List(ctx)
		if err != nil {
			return "", err
		}
		for _, r := range repos {
			f.ProjectIDs = append(f.ProjectIDs, r.ID)
		}
	} else {
		f.ProjectIDs = []string{sc.ProjectID}
	}
	if assistant != "" { // the assistant's chats are each person's own
		f.HideOthersIn = assistant
		if c, err := t.store.Chat().GetConversation(ctx, sc.ConversationID); err == nil {
			f.Me = c.CreatedBy
		}
	}
	hits, err := t.store.Chat().SearchMessages(ctx, query, f)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("Không thấy \"%s\" trong %d ngày gần đây.", query, days), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d kết quả cho \"%s\" (mới nhất trong %d ngày):\n", len(hits), query, days)
	for _, h := range hits {
		who := h.Author
		if who == "" {
			who = h.Role
		}
		fmt.Fprintf(&b, "- %s · %s · %s: %s\n  /projects/%s?tab=chat&c=%s&m=%s\n", h.CreatedAt.Local().Format("02/01 15:04"), cmp.Or(h.Title, "(chưa đặt tên)"), who,
			strings.ReplaceAll(h.Snippet, "\n", " "), h.ProjectID, h.ConversationID, h.MessageID)
	}
	return b.String(), nil
}

// burnChat: the run is a Burn's conversation, where the burn_* tools work.
func (t *Toolbox) burnChat(sc Scope) bool {
	if sc.ConversationID == "" {
		return false
	}
	c, err := t.store.Chat().GetConversation(context.Background(), sc.ConversationID)
	return err == nil && c.Purpose == "burn"
}
