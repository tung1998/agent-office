package burn

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
)

// Tool is one of the agent's Burn tools, called from the Burn's conversation
// (burn_add, burn_pick, burn_skip, burn_list…), or from a piece's work chat
// (ADR-116) for that piece only: a scan records pieces and ends with
// burn_scan_done (ADR-130), a worker reports burn_done or burn_fail.
func (s *Service) Tool(ctx context.Context, sc actions.Scope, name string, in ToolInput) (string, error) {
	switch name { // from any chat of the project
	case "burn_quest":
		return s.giveQuest(ctx, sc, in)
	case "burn_quests":
		return s.quests(ctx, sc.ProjectID, in.Owner)
	case "burn_assign":
		return s.assign(ctx, sc.ProjectID, in)
	case "burn_drop":
		return s.drop(ctx, sc, in)
	}
	b, err := s.store.Burn().SessionByConversation(ctx, sc.ConversationID)
	if err != nil || sc.ConversationID == "" {
		return "", errors.New("các công cụ burn_* chỉ dùng trong hội thoại Burn")
	}
	if name == "burn_add" && b.ConversationID == sc.ConversationID {
		return s.record(ctx, b, "", in)
	}
	if name == "burn_list" { // its own chat or a piece's (a reviewer's only reads)
		if b.ConversationID != sc.ConversationID {
			if _, err := s.workItem(ctx, b.ID, sc.ConversationID); err != nil {
				return "", errors.New("các công cụ burn_* chỉ dùng trong hội thoại Burn")
			}
		}
		return s.list(ctx, b, in.What)
	}
	it, err := s.store.Burn().Item(ctx, strings.TrimSpace(in.Item))
	if b.ConversationID != sc.ConversationID { // a piece's work chat (a reviewer's only reads)
		own, oerr := s.workItem(ctx, b.ID, sc.ConversationID)
		switch {
		case oerr != nil:
			return "", errors.New("các công cụ burn_* chỉ dùng trong hội thoại Burn")
		case name == "burn_add" && hunting(own):
			return s.record(ctx, b, own.ID, in)
		case name == "burn_add":
			return s.record(ctx, b, "", in)
		case hunting(own) && name != "burn_scan_done":
			return "", fmt.Errorf("lượt quét %s chỉ ghi việc (burn_add) và kết thúc bằng burn_scan_done, không làm việc", own.ID)
		case name != "burn_done" && name != "burn_fail" && name != "burn_scan_done":
			return "", fmt.Errorf("chat của việc %s chỉ dùng burn_done/burn_fail cho chính nó", own.ID)
		case err != nil || it.ID != own.ID:
			return "", fmt.Errorf("chat này chỉ báo cho việc %s", own.ID)
		case name == "burn_scan_done":
			return s.scanDone(ctx, b, own, in)
		}
	}
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
		if s.reviews(ctx, b, "result") { // the reviewer has the last word (ADR-112)
			it.Status = "review"
		}
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
	if name == "burn_pick" { // a slot free takes it at once (ADR-126: no waiting)
		s.wake(b.ProjectID)
	}
	return fmt.Sprintf("Đã ghi: %s → %s.", it.Title, it.Status), nil
}

// giveQuest is burn_quest: an agent of any chat puts a quest on the board,
// as the person would on the dashboard.
func (s *Service) giveQuest(ctx context.Context, sc actions.Scope, in ToolInput) (string, error) {
	if strings.TrimSpace(in.Detail) == "" {
		return "", errors.New("quest cần mô tả (detail) đủ để làm mà không phải hỏi lại")
	}
	owner, err := s.ownerKey(ctx, sc.ProjectID, in.Owner)
	if err != nil {
		return "", err
	}
	if in.Owner == "" { // the caller, when it is one of the project's agents
		owner, _ = s.ownerKey(ctx, sc.ProjectID, sc.Agent)
	}
	added, _, err := s.AddQuests(ctx, sc.ProjectID, []Quest{{Title: in.Title, Detail: in.Detail, Owner: owner}}, false, "agent:"+sc.Agent)
	if err != nil {
		return "", err
	}
	if added == 0 {
		return "", fmt.Errorf("Burn đã có quest đang mở tên %q", strings.TrimSpace(in.Title))
	}
	if b, _ := s.Session(ctx, sc.ProjectID); b.Active() {
		return "Quest added to the board; the running Burn takes it next (quests go one at a time, oldest first).", nil
	}
	return "Quest added to the board; it waits there for the person to give it to an agent (or for the Burn to be turned on).", nil
}

// quests is burn_quests: the Burn's state, its run's work not merged yet
// and what it found on its own (ADR-196), and its quests, latest last, with
// who follows each up; owner keeps only theirs (ADR-197).
func (s *Service) quests(ctx context.Context, projectID, owner string) (string, error) {
	b, err := s.Session(ctx, projectID)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Burn: %s\n", b.State)
	items := []storage.BurnItem{}
	if b.ID != "" {
		items, _ = s.store.Burn().Items(ctx, b.ID)
	}
	if r, err := s.store.Repos().Get(ctx, projectID); err == nil {
		if n := unmerged(ctx, r.Path, b.RunBranch); n > 0 {
			fmt.Fprintf(&sb, "Run branch %s: %d commits not merged into the project's branch yet; once reviewed, merge with propose_action git_merge.\n", b.RunBranch, n)
		}
	}
	own := map[string]int{}
	for _, it := range items {
		if it.Kind != KindQuest && it.Kind != "" {
			own[it.Status]++
		}
	}
	if len(own) > 0 {
		fmt.Fprintf(&sb, "Its own finds: %d open, %d done, %d failed, %d skipped.\n",
			own["found"]+own["queued"]+own["doing"]+own["paused"]+own["review"]+own[StatusHanded], own["done"], own["failed"], own["skipped"])
	}
	if owner != "" {
		key, err := s.ownerKey(ctx, projectID, owner)
		if err != nil {
			return "", err
		}
		owner = key
	}
	items = slices.DeleteFunc(items, func(it storage.BurnItem) bool { return it.Kind != KindQuest || (owner != "" && it.Owner != owner) })
	if len(items) > maxQuestList {
		items = items[len(items)-maxQuestList:]
	}
	if len(items) == 0 {
		return sb.String() + "No quests.", nil
	}
	for _, it := range items {
		fmt.Fprintf(&sb, "- %s [%s] (%s) %s", it.ID, it.Status, cmp.Or(it.Owner, "default agent"), it.Title)
		if it.Summary != "" {
			fmt.Fprintf(&sb, ": %s", it.Summary)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ownerKey is the key of the project's agent ref names (its key or its
// name, case aside); "" stays "" (the default agent).
func (s *Service) ownerKey(ctx context.Context, projectID, ref string) (string, error) {
	if ref = strings.TrimSpace(ref); ref == "" {
		return "", nil
	}
	agents, err := s.store.Agents().List(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if strings.EqualFold(a.Key, ref) || strings.EqualFold(a.Name, ref) {
			return a.Key, nil
		}
	}
	return "", fmt.Errorf("project không có agent %q để phụ trách quest", ref)
}

// assign is burn_assign: a quest (item), or every open quest of an owner
// (from), goes to another owner: a handover (ADR-197).
func (s *Service) assign(ctx context.Context, projectID string, in ToolInput) (string, error) {
	to, err := s.ownerKey(ctx, projectID, in.Owner)
	if err != nil {
		return "", err
	}
	if (in.Item == "") == (in.From == "") {
		return "", errors.New("burn_assign cần item (một quest) hoặc from (mọi quest đang mở của một người)")
	}
	from := ""
	if in.From != "" {
		if from, err = s.ownerKey(ctx, projectID, in.From); err != nil {
			return "", err
		}
	}
	b, err := s.Session(ctx, projectID)
	if err != nil || b.ID == "" {
		return "", errors.New("bảng việc chưa có quest nào")
	}
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return "", err
	}
	n := 0
	for _, it := range items {
		if it.Kind != KindQuest || it.Owner == to {
			continue
		}
		if in.Item != "" && it.ID != strings.TrimSpace(in.Item) || in.From != "" && (it.Owner != from || !openStatus[it.Status]) {
			continue
		}
		if err := s.setOwner(ctx, it, to); err != nil {
			return "", err
		}
		n++
	}
	if in.Item != "" && n == 0 {
		return "", fmt.Errorf("không có quest %q để giao (hoặc đã là của %s)", in.Item, cmp.Or(to, "agent mặc định"))
	}
	return fmt.Sprintf("%d quest now followed up by %s.", n, cmp.Or(to, "the default agent")), nil
}

// drop is burn_drop: a quest taken off the board with why (a duplicate, one
// a newer plan replaced), as the person skips one on the dashboard. A Burn
// or an agent on it meanwhile does not merge or close it (they see it moved).
func (s *Service) drop(ctx context.Context, sc actions.Scope, in ToolInput) (string, error) {
	why := strings.TrimSpace(in.Reason)
	if why == "" {
		return "", errors.New("hãy ghi lý do bỏ quest (reason)")
	}
	b, err := s.Session(ctx, sc.ProjectID)
	if err != nil || b.ID == "" {
		return "", errors.New("bảng việc chưa có quest nào")
	}
	it, err := s.store.Burn().Item(ctx, strings.TrimSpace(in.Item))
	if err != nil || it.SessionID != b.ID || it.Kind != KindQuest {
		return "", fmt.Errorf("không có quest %q trên bảng việc này", in.Item)
	}
	if !openStatus[it.Status] {
		return "", fmt.Errorf("quest %s đã %s, không cần bỏ", it.ID, it.Status)
	}
	from := it.Status
	it.Status, it.Summary = "skipped", "Bỏ ("+sc.Agent+"): "+why
	if err := s.store.Burn().UpdateItemFrom(ctx, it, from); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return "", fmt.Errorf("quest %s vừa đổi trạng thái ở nơi khác, hãy xem lại bảng việc", it.ID)
		}
		return "", err
	}
	return fmt.Sprintf("Dropped: %s.", it.Title), nil
}

// setOwner changes only its owner: a Burn moving it meanwhile is not undone
// (compare-and-swap on its status, read again on a race).
func (s *Service) setOwner(ctx context.Context, it storage.BurnItem, owner string) error {
	for range 5 {
		cur, err := s.store.Burn().Item(ctx, it.ID) // as it is now, not as listed
		if err != nil {
			return err
		}
		cur.Owner = owner
		if err = s.store.Burn().UpdateItemFrom(ctx, cur, cur.Status); !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// maxQuestList: the latest quests burn_quests shows.
const maxQuestList = 30

// workItem is the piece of session whose work chat conversationID is.
func (s *Service) workItem(ctx context.Context, sessionID, conversationID string) (storage.BurnItem, error) {
	items, err := s.store.Burn().Items(ctx, sessionID)
	if err != nil {
		return storage.BurnItem{}, err
	}
	for _, it := range items {
		if it.WorkConversationID == conversationID {
			return it, nil
		}
	}
	return storage.BurnItem{}, storage.ErrNotFound
}

// ToolInput is what the Burn tools take.
type ToolInput struct {
	Title, Kind, Detail, Item, Summary, Reason string
	What                                       string // burn_list: open | closed | scanned | map
	Scanned                                    string // burn_scan_done: the areas looked at
	Map                                        string // burn_scan_done: the code map, updated (ADR-130)
	Lessons                                    string // burn_scan_done: the lessons, updated (ADR-131)
	Coverage                                   string // burn_scan_done: the coverage plan, updated (ADR-141)
	Priority                                   string // burn_add: high | normal | low
	Owner                                      string // burn_quest, burn_assign: who follows it up; burn_quests: only theirs (ADR-197)
	From                                       string // burn_assign: every open quest of this owner
}

// list is what burn_list gives (ADR-121): the Burn's data kept out of the
// coordination prompt, read when needed.
func (s *Service) list(ctx context.Context, b storage.BurnSession, what string) (string, error) {
	if what == "map" { // a worker reads it when it needs it, not in every prompt (ADR-146)
		if b.CodeMap == "" {
			return "No code map yet.", nil
		}
		return b.CodeMap, nil
	}
	if what == "scanned" {
		if b.Scanned == "" {
			return "No scan has recorded its areas yet.", nil
		}
		return "Areas earlier scans looked at (oldest first):\n" + b.Scanned, nil
	}
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return "", err
	}
	closed := map[string]bool{"done": true, "skipped": true, "failed": true}
	var sb strings.Builder
	n := 0
	for _, it := range items {
		if closed[it.Status] != (what == "closed") {
			continue
		}
		n++
		fmt.Fprintf(&sb, "- %s [%s, %s] %s", it.ID, it.Kind, it.Status, it.Title)
		switch {
		case what == "closed" && it.Summary != "":
			fmt.Fprintf(&sb, " — %s", oneLine(it.Summary, 160))
		case what != "closed" && it.Detail != "":
			fmt.Fprintf(&sb, "\n  %s", oneLine(it.Detail, 600))
		}
		sb.WriteString("\n")
	}
	if n == 0 {
		return "None.", nil
	}
	return sb.String(), nil
}
