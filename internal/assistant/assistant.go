// Package assistant sets up the office assistant (ADR-046): a hidden system
// project "Office" with one agent that works across projects through the
// office tools in office scope. Its chats reuse the chat engine as they are.
package assistant

import (
	"context"
	"errors"
	"os"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
)

const settingKey = "office_assistant_project"

// Instructions of the assistant agent.
const Instructions = `Bạn là trợ lý của toàn office (agent-office), không thuộc project nào.
- Việc của bạn: trả lời về tình hình các project, thống kê và báo cáo (chi phí, job, lỗi), cài đặt (tự động hóa, agent, quyền, giám sát, kết nối AI, ngân sách), điều phối việc sang đúng project.
- Luôn gọi projects trước để biết project nào; chưa rõ project thì hỏi lại người dùng.
- Số liệu: jobs_query, usage_summary; tình hình vận hành: ops_overview/process_logs/monitor_detail với project.
- Mọi thay đổi đều qua thẻ duyệt: propose_change (xem describe/list/get trước), run_automation. Không nói là đã làm khi mới đề xuất.
- Việc cần đọc hay sửa code thì dùng handoff để chuyển sang Chat của project đó (đưa người dùng liên kết); bạn không sửa code.
- Trả lời ngắn gọn bằng tiếng Việt, có số liệu thật.`

// ID is the assistant's project id ("" = not set up).
func ID(ctx context.Context, store storage.Store) string {
	var id string
	if ok, _ := store.Settings().Get(ctx, settingKey, &id); ok {
		return id
	}
	return ""
}

// Ensure sets the assistant up once (dir is its empty working folder) and
// returns its project id.
func Ensure(ctx context.Context, store storage.Store, org *orgmodel.Service, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if id := ID(ctx, store); id != "" {
		if _, err := store.Repos().Get(ctx, id); err == nil {
			return id, nil
		}
	}
	repo, err := store.Repos().Create(ctx, storage.Repo{Name: "Office", Path: dir, Description: "Trợ lý toàn office (dự án hệ thống, không hiện trong danh sách)"})
	if err != nil {
		return "", err
	}
	tpl, err := store.OrgModels().GetTemplateByKey(ctx, "solo")
	if err != nil {
		return "", errors.New("thiếu mô hình mẫu solo để dựng trợ lý")
	}
	m, err := org.ApplyToRepo(ctx, repo.ID, tpl.ID, false)
	if err != nil {
		return "", err
	}
	agents, err := store.Agents().List(ctx, m.ID)
	if err != nil || len(agents) == 0 {
		return "", errors.New("trợ lý chưa có agent")
	}
	a := agents[0]
	a.Name, a.Key, a.Role = "Trợ lý office", "office-assistant", "Trợ lý toàn office: điều phối, thống kê, cài đặt"
	a.Instructions = Instructions
	a.ModelTier = storage.TierBalanced
	a.Permissions = storage.Permissions{Level: "read", ReadOnly: true}
	a.Avatar = storage.Avatar{Color: "violet", Icon: "i-lucide-sparkles"}
	if err := store.Agents().Update(ctx, a); err != nil {
		return "", err
	}
	return repo.ID, store.Settings().Set(ctx, settingKey, repo.ID)
}

// The assistant's rights (ADR-059).
const (
	ModeAnswer = "answer" // reads the office and answers; proposes nothing
	ModeManage = "manage" // + proposes changes on approval cards, hands work to projects (the default)
	ModeAdmin  = "admin"  // + every command on the machine office runs on (for an admin of the office)
)

const modeKey = "assistant.mode"

// Mode is the assistant's rights as set (ModeManage when never set).
func Mode(ctx context.Context, store storage.Store) string {
	var m string
	if ok, _ := store.Settings().Get(ctx, modeKey, &m); ok && (m == ModeAnswer || m == ModeManage || m == ModeAdmin) {
		return m
	}
	return ModeManage
}

// SetMode sets the assistant's rights.
func SetMode(ctx context.Context, store storage.Store, mode string) error {
	if mode != ModeAnswer && mode != ModeManage && mode != ModeAdmin {
		return errors.New("quyền của trợ lý phải là answer, manage hoặc admin")
	}
	return store.Settings().Set(ctx, modeKey, mode)
}

// Powers is what the assistant may do for one person: administrator only for
// an admin of the office, anyone else gets it as manage.
func Powers(mode string, admin bool) string {
	if mode == ModeAdmin && !admin {
		return ModeManage
	}
	return mode
}
