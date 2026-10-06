package burn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
)

// Tool is one of the agent's Burn tools (burn_add, burn_pick, burn_skip,
// burn_done, burn_fail), called from the Burn's conversation only.
func (s *Service) Tool(ctx context.Context, sc actions.Scope, name string, in ToolInput) (string, error) {
	b, err := s.store.Burn().SessionByConversation(ctx, sc.ConversationID)
	if err != nil {
		return "", errors.New("các công cụ burn_* chỉ dùng trong hội thoại Burn")
	}
	if name == "burn_add" {
		return s.add(ctx, b, in)
	}
	it, err := s.store.Burn().Item(ctx, strings.TrimSpace(in.Item))
	if err != nil || it.SessionID != b.ID {
		return "", fmt.Errorf("không có việc %q trong Burn này", in.Item)
	}
	fromStatus := it.Status
	switch name {
	case "burn_pick":
		if it.Status != "found" {
			return "", fmt.Errorf("việc %s đang %s, không chọn lại được", it.ID, it.Status)
		}
		it.Status = "queued"
	case "burn_skip":
		it.Status, it.Summary = "skipped", strings.TrimSpace(in.Reason)
	case "burn_done":
		if strings.TrimSpace(in.Summary) == "" {
			return "", errors.New("hãy ghi tóm tắt đã làm gì")
		}
		it.Status, it.Summary = "done", strings.TrimSpace(in.Summary)
	case "burn_fail":
		it.Status, it.Summary = "failed", strings.TrimSpace(in.Reason)
	default:
		return "", fmt.Errorf("không có công cụ %s", name)
	}
	// compare-and-swap theo status đã đọc: 2 lệnh đổi trạng thái cùng item
	// chạy gần như đồng thời (vd 2 burn_pick) không được phép cùng thành công
	if err := s.store.Burn().UpdateItemFrom(ctx, it, fromStatus); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return "", fmt.Errorf("việc %s vừa đổi trạng thái ở nơi khác, hãy xem lại danh sách", it.ID)
		}
		return "", err
	}
	return fmt.Sprintf("Đã ghi: %s → %s.", it.Title, it.Status), nil
}

// ToolInput is what the Burn tools take.
type ToolInput struct {
	Title, Kind, Detail, Item, Summary, Reason string
}

func (s *Service) add(ctx context.Context, b storage.BurnSession, in ToolInput) (string, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return "", errors.New("hãy ghi tiêu đề việc")
	}
	kind := strings.TrimSpace(in.Kind)
	if !Kinds[kind] {
		kind = "upgrade"
	}
	items, _ := s.store.Burn().Items(ctx, b.ID)
	for _, it := range items { // the same piece twice: once
		if strings.EqualFold(strings.Join(strings.Fields(it.Title), " "), strings.Join(strings.Fields(title), " ")) {
			return fmt.Sprintf("Đã có việc này: %s (%s).", it.ID, it.Status), nil
		}
	}
	it, err := s.store.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: oneLine(title, 160), Kind: kind, Detail: strings.TrimSpace(in.Detail)})
	if err != nil {
		return "", err
	}
	return "Đã ghi việc " + it.ID + ".", nil
}
