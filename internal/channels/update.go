package channels

import (
	"context"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Updater is office's self-update as a bot drives it (/update, ADR-255).
type Updater interface {
	// Status says in a line where an update stands.
	Status(ctx context.Context) string
	// WhenIdle builds now and restarts once no work runs; by: who asked.
	WhenIdle(ctx context.Context, by string) (string, error)
	// Cancel drops the update in progress.
	Cancel() error
}

// SetUpdater gives the bots /update (nil: office cannot update itself).
func (m *Manager) SetUpdater(u Updater) { m.updater = u }

// updateCommand runs /update [status|cancel]: the bot's admins start or
// cancel an update that waits for office to be idle; anyone sees its status.
func (m *Manager) updateCommand(ctx context.Context, ch storage.Channel, in Incoming, arg, who string) string {
	if m.updater == nil {
		return "Office này không tự cập nhật được: cần chạy dưới `office run` và có mã nguồn."
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "status", "trang-thai", "?":
		return m.updater.Status(ctx)
	case "cancel", "huy", "hủy":
		if !MayDecide(ch, in.UserID) {
			return "Chỉ người trong danh sách Admin của bot được hủy cập nhật."
		}
		if err := m.updater.Cancel(); err != nil {
			return err.Error()
		}
		return "Đã hủy bản cập nhật: office giữ bản đang chạy."
	case "":
		if !MayDecide(ch, in.UserID) {
			return "Chỉ người trong danh sách Admin của bot được cập nhật office.\n" + m.updater.Status(ctx)
		}
		msg, err := m.updater.WhenIdle(ctx, ch.Kind+":"+who)
		if err != nil {
			return "Không cập nhật được: " + err.Error()
		}
		return msg
	}
	return "Dùng /update (cập nhật khi rảnh), /update status hoặc /update cancel."
}
