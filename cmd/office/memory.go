package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/storage"
)

// compactNotes rewrites an agent's notes shorter with a fast model, no tools.
func compactNotes(st storage.Store, engine *chat.Engine) memory.Compactor {
	return func(ctx context.Context, projectID, agentID string, items []storage.Memory) ([]string, error) {
		project, err := st.Repos().Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		agent, err := st.Agents().Get(ctx, agentID)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		for _, m := range items {
			fmt.Fprintf(&b, "- %s\n", strings.ReplaceAll(m.Text, "\n", " "))
		}
		prompt := "Đây là sổ ghi nhớ của một agent ở một project. Gộp và rút gọn lại: bỏ ý trùng, ý đã lỗi thời hoặc mâu thuẫn (giữ ý mới hơn, ở dưới), " + // i18n-ignore
			"gộp ý cùng chủ đề, giữ nguyên tên file, lệnh, quy ước. Tổng cộng dưới 2000 ký tự. " + // i18n-ignore
			"Chỉ trả về danh sách, mỗi ý một dòng bắt đầu bằng \"- \", không thêm gì khác.\n\n" + b.String() // i18n-ignore
		res, err := engine.Invoke(chat.WithNoTools(chat.WithModelTier(ctx, storage.TierFast)), project, agent, prompt, nil, "memory_compact", nil)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, l := range strings.Split(res.Text, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "- ") {
				out = append(out, strings.TrimPrefix(l, "- "))
			}
		}
		return out, nil
	}
}
