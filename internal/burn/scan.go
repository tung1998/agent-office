package burn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/textrank"
)

// A Burn's scan (ADR-130) is a piece not claimed (no kind): in its own chat
// and worktree it follows a set process (orient by the code map, find,
// challenge, record) and only records pieces (burn_add, status found) with a
// brief a worker starts from; it changes no code. It ends with
// burn_scan_done, which keeps the areas it looked at and its code map and
// drops the scan. Workers then take the pieces from found, quests first,
// each its own piece (burn/work.md). One scan at a time, each recording up
// to scanBatch pieces, with no cap on the pieces waiting (ADR-141).
//
// The coverage plan (ADR-141) is the scans' memory of what is left: a
// checklist of every area the template covers, the first scan writing it,
// each next one taking the unchecked items in order and checking them off.
// Scans are over once every item is checked and a scan finds nothing more,
// or after maxNones scans in a row that moved nothing on (no piece recorded,
// no item checked): the Burn does what is left, then stops.

// huntTitle is a scan's title.
const huntTitle = "Đang quét…"

// scanBatch: pieces one scan records at most; the next scan goes on.
const scanBatch = 10

// maxNones: scans in a row that moved nothing on before the Burn scans no
// more this run.
const maxNones = 3

// maxCoverage caps the coverage plan kept (ADR-141). A plan past it has
// its checked items' results shortened, never items cut (ADR-147).
const maxCoverage = 16000

// resultCuts: how much of "what came of it" a checked item keeps, step by
// step, while the plan is past maxCoverage (0: the area alone).
var resultCuts = []int{120, 60, 0}

// maxChecks: coverage items one scan may check off. It is no stop sign: a
// scan goes on item after item until it has recorded scanBatch pieces or
// the plan is done; it only catches a plan ticked wholesale from memory
// (ADR-141, ADR-145), with each item checked naming what was read.
const maxChecks = 25

// maxCodeMap caps the code map kept (ADR-130).
const maxCodeMap = 8000

// What a scan's prompt carries: the latest pieces recorded, the latest
// areas looked at.
const (
	maxTakenInScan   = 120
	maxScannedInScan = 3 // the coverage plan says where to go on; these only show the latest
)

// hunting: a scan (a piece with no kind).
func hunting(it storage.BurnItem) bool { return it.Kind == "" }

// item reads a coverage plan's line: its text after the box, and whether
// it is an item and checked.
func item(line string) (text string, isItem, checked bool) {
	l := strings.TrimLeft(strings.TrimSpace(line), "-*+ ")
	switch {
	case strings.HasPrefix(l, "[ ]"):
		return strings.TrimSpace(l[3:]), true, false
	case strings.HasPrefix(l, "[x]"), strings.HasPrefix(l, "[X]"):
		return strings.TrimSpace(l[3:]), true, true
	}
	return "", false, false
}

// uncheck sets every item of a plan back to not looked at: a new run
// covers it all again.
func uncheck(plan string) string {
	lines := strings.Split(plan, "\n")
	for i, line := range lines {
		if _, ok, checked := item(line); ok && checked {
			lines[i] = strings.Replace(strings.Replace(line, "[x]", "[ ]", 1), "[X]", "[ ]", 1)
		}
	}
	return strings.Join(lines, "\n")
}

// planProblem is why a scan's new coverage plan is refused (ADR-141): an
// item that bundles several areas, one checked off this scan without the
// file it read (ADR-145), or more items checked off than one scan can look
// at for real.
func planProblem(before, after string) string {
	seen := map[string]bool{} // checked before this scan
	for _, line := range strings.Split(before, "\n") {
		if text, ok, checked := item(line); ok && checked {
			area, _, _ := strings.Cut(text, " — ")
			seen[strings.TrimSpace(area)] = true
		}
	}
	for _, line := range strings.Split(after, "\n") {
		text, ok, checked := item(line)
		if !ok {
			continue
		}
		area, result, _ := strings.Cut(text, " — ")
		if strings.Count(area, ",") >= 2 {
			return fmt.Sprintf("mỗi mục chỉ một vùng (một màn, một gói, một tính năng), tách dòng %q thành từng mục", oneLine(area, 80))
		}
		if checked && !seen[strings.TrimSpace(area)] && !readRe.MatchString(result) && !naRe.MatchString(result) {
			return fmt.Sprintf("mục %q đánh dấu mà không ghi file đã đọc trong lượt này: ghi \"- [x] vùng — kết quả (file:dòng đã đọc)\"; chưa đọc thật thì để [ ]", oneLine(area, 80))
		}
	}
	was, _ := coverageCount(before)
	now, _ := coverageCount(after)
	if now-was > maxChecks {
		return fmt.Sprintf("lượt này đánh dấu %d mục, tối đa %d: chỉ đánh dấu mục đã xem thật trong lượt này, phần còn lại để [ ] cho lượt sau", now-was, maxChecks)
	}
	if left(before)-left(after) > maxChecks { // items not looked at dropped, or merged away
		return fmt.Sprintf("còn %d mục chưa xem trước lượt này, giờ còn %d: không bỏ hay gộp mục chưa xem, mỗi lượt bớt tối đa %d", left(before), left(after), maxChecks)
	}
	return ""
}

// mergeCoverage applies what a scan sent to the plan (ADR-146): a scan
// sends only the lines it changed or added, not the whole plan (output
// tokens), the whole plan the first time. A line for an area the plan has
// replaces it — but an item checked this run stays checked; a new area is
// added at the end; the plan's other lines stay as they were.
func mergeCoverage(before, after string) string {
	if _, total := coverageCount(before); total == 0 {
		return after
	}
	lines := strings.Split(strings.TrimRight(before, "\n"), "\n")
	at := map[string]int{} // area → its line in the plan
	for i, line := range lines {
		if text, ok, _ := item(line); ok {
			if _, dup := at[areaOf(text)]; !dup {
				at[areaOf(text)] = i
			}
		}
	}
	for _, line := range strings.Split(after, "\n") {
		text, ok, checked := item(line)
		if !ok {
			continue
		}
		i, known := at[areaOf(text)]
		switch {
		case !known:
			at[areaOf(text)] = len(lines)
			lines = append(lines, line)
		case checked:
			lines[i] = line
		default: // [ ]: a checked item is not set back; an unchecked one may be reworded
			if _, _, was := item(lines[i]); !was {
				lines[i] = line
			}
		}
	}
	return strings.Join(lines, "\n")
}

// areaOf is an item's area: its text before " — what came of it".
func areaOf(text string) string {
	area, _, _ := strings.Cut(text, " — ")
	return strings.ToLower(strings.TrimSpace(area))
}

// The plan says which template wrote it: a plan written for another one
// (a list of packages for bugs, say, under the ideas template) would steer
// the scans the wrong way, so a run of another template starts afresh.
const tagPrefix = "<!-- burn template: "

// tagged is plan with its template line, for template t, on top.
func tagged(plan, t string) string {
	var lines []string
	for _, line := range strings.Split(plan, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), tagPrefix) {
			lines = append(lines, line)
		}
	}
	return tagPrefix + t + " -->\n" + strings.TrimSpace(strings.Join(lines, "\n"))
}

// planTemplate is the template plan was written for ("" untold).
func planTemplate(plan string) string {
	for _, line := range strings.Split(plan, "\n") {
		if r, ok := strings.CutPrefix(strings.TrimSpace(line), tagPrefix); ok {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(r), "-->"))
		}
	}
	return ""
}

// readRe is what an item checked off names of what was read: a file, or a
// screenshot.
var readRe = regexp.MustCompile(pathRe.String() + `|\.(?:png|jpe?g)\b`)

// naRe is an item checked off as not there to read: the file does not
// exist, it does not apply, or it repeats another item (ADR-147), so a
// wrong item does not stay unchecked for good.
var naRe = regexp.MustCompile(`(?i)không tồn tại|không áp dụng|trùng (?:với )?mục|does not exist|not applicable|duplicate of`)

// dupRe is an item checked off as only repeating another, already-covered
// one ("trùng mục …" / "duplicate of …"): once recorded, the line itself is
// pure overhead (the area it names is accounted for elsewhere), unlike
// "không tồn tại"/"does not exist" or "không áp dụng"/"not applicable",
// which ARE the finding and must stay.
var dupRe = regexp.MustCompile(`(?i)trùng (?:với )?mục|duplicate of`)

// fitCoverage keeps plan within maxCoverage by shortening what its checked
// items say came of them, step by step, never dropping an item: an item cut
// off the end would be looked at again, and a scan whose new items were cut
// would count as moving nothing on (ADR-147). fits is false when even the
// bare areas are past the cap.
func fitCoverage(plan string) (string, bool) {
	if len([]rune(plan)) <= maxCoverage {
		return plan, true
	}
	lines := dedupeItems(strings.Split(plan, "\n"))
	if plan = strings.Join(lines, "\n"); len([]rune(plan)) <= maxCoverage {
		return plan, true
	}
	lines = dropDuplicateMarked(lines)
	if plan = strings.Join(lines, "\n"); len([]rune(plan)) <= maxCoverage {
		return plan, true
	}
	for _, keep := range resultCuts {
		for i, line := range lines {
			text, ok, checked := item(line)
			if !ok || !checked {
				continue
			}
			area, result, has := strings.Cut(text, " — ")
			if !has {
				continue
			}
			r := []rune(strings.TrimSpace(result))
			switch {
			case keep == 0:
				lines[i] = "- [x] " + strings.TrimSpace(area)
			case len(r) > keep:
				lines[i] = "- [x] " + strings.TrimSpace(area) + " — " + string(r[:keep-1]) + "…"
			}
		}
		if plan = strings.Join(lines, "\n"); len([]rune(plan)) <= maxCoverage {
			return plan, true
		}
	}
	return plan, false
}

// dedupeItems drops an item line that repeats one already kept, word for
// word: over many scans the same area piles up with slightly different
// scans' phrasing but sometimes the exact same line twice, pushing the plan
// past maxCoverage with no new information (ADR-141). A line that differs at
// all (even a word) is kept, since telling real near-duplicates apart needs
// reading the code, not guessing from text alone.
func dedupeItems(lines []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, ok, _ := item(line); ok {
			key := strings.TrimSpace(line)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, line)
	}
	return out
}

// dropDuplicateMarked removes a checked item whose result says it merely
// repeats another, already-covered area (dupRe): a worker marking real
// duplicates it found (maxChecks of them a scan, like any other check-off,
// via planProblem) is the one way to actually shrink an over-cap plan,
// since every other item must stay (ADR-147).
func dropDuplicateMarked(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if text, ok, checked := item(line); ok && checked {
			if _, result, has := strings.Cut(text, " — "); has && dupRe.MatchString(result) {
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

// coverageCount reads a coverage plan: its items ("- [ ]" or "- [x]"
// lines) and how many are checked.
func coverageCount(plan string) (checked, total int) {
	for _, line := range strings.Split(plan, "\n") {
		if _, ok, done := item(line); ok {
			total++
			if done {
				checked++
			}
		}
	}
	return checked, total
}

// scanPrompt is a scan's turn (burn/scan.md).
func scanPrompt(b storage.BurnSession, it storage.BurnItem, items []storage.BurnItem, again bool) string {
	var pieces []storage.BurnItem
	for i := len(items) - 1; i >= 0; i-- { // latest first, so a piece tried again shows its last try
		if x := items[i]; x.ID != it.ID && !hunting(x) {
			pieces = append(pieces, x)
		}
	}
	// this run's pieces and the open ones first: a run past the cap would
	// otherwise push its own early pieces out, and the scan record them again;
	// earlier runs' pieces fill what room is left
	var mine, rest []string
	for _, g := range groupByPiece(pieces) {
		line := fmt.Sprintf("[%s%s] %s", g.it.Status, times(g.n), oneLine(g.it.Title, 120))
		if g.it.Status == "found" && g.it.Kind != KindQuest {
			line = fmt.Sprintf("[found%s %s] %s", times(g.n), g.it.ID, oneLine(g.it.Title, 120))
		}
		if g.it.RunBranch == b.RunBranch || openPiece(g.it.Status) {
			mine = append(mine, line)
		} else {
			rest = append(rest, line)
		}
	}
	taken := append(mine, rest...)
	if len(taken) > maxTakenInScan {
		taken = taken[:maxTakenInScan]
	}
	slices.Reverse(taken) // oldest first, the latest last
	var scanned []string
	if b.Scanned != "" {
		scanned = strings.Split(strings.TrimSpace(b.Scanned), "\n")
	}
	if len(scanned) > maxScannedInScan {
		scanned = scanned[len(scanned)-maxScannedInScan:]
	}
	return prompts.Render("burn/scan", struct {
		ID, Focus, Order, Hunt, Map, Lessons, Coverage string
		FocusLooks, Taken, Scanned, Setbacks, Changed  []string
		Batch, Left, MaxChecks                         int
		Again                                          bool
	}{it.ID, b.Focus, b.Order, hunt(b), b.CodeMap, b.Lessons, b.Coverage, looks(b), taken, scanned, setbacks(items), changed(b, items), scanBatch, left(b.Coverage), maxChecks, again})
}

// openPiece: a piece still waiting or being done.
func openPiece(status string) bool {
	switch status {
	case "found", "queued", "doing", "paused", "review", StatusHanded:
		return true
	}
	return false
}

// left: the items of a coverage plan not checked yet.
func left(plan string) int {
	checked, total := coverageCount(plan)
	return total - checked
}

// priorities: what a scan may say of a piece, as the order it is done in.
var priorities = map[string]int{"high": 2, "normal": 1, "low": 0}

// record adds a piece found (status found, in the run): none twice, a scan
// none past scanBatch. from is the scan recording it ("" = the Burn's chat
// or a worker's), counted for burn_scan_done.
func (s *Service) record(ctx context.Context, b storage.BurnSession, from string, in ToolInput) (string, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return "", errors.New("hãy ghi tiêu đề việc")
	}
	kind := strings.TrimSpace(in.Kind)
	if !Kinds[kind] {
		kind = "upgrade"
	}
	items, _ := s.store.Burn().Items(ctx, b.ID)
	for _, it := range items { // the same piece twice (or once folded in): once
		if !hunting(it) && (sameTitle(it.Title, title) || strings.Contains(strings.ToLower(it.Detail), strings.ToLower(foldMark+oneLine(questKey(title), 160)+"\n"))) {
			return fmt.Sprintf("Đã có việc này: %s (%s).", it.ID, it.Status), nil
		}
	}
	n, why := failedBefore(items, title)
	if n >= maxTries && from != "" { // a scan recording it again (ADR-143)
		return "", fmt.Errorf("việc này đã thất bại %d lần (lần cuối: %s): không ghi lại. Nếu nguyên nhân nằm ở quy trình hay môi trường, ghi một việc sửa nguyên nhân đó", n, oneLine(why, 200))
	}
	if from != "" {
		s.mu.Lock()
		cnt := s.found[from]
		s.mu.Unlock()
		if cnt >= scanBatch {
			return "", fmt.Errorf("lượt quét này đã ghi đủ %d việc: cập nhật coverage rồi kết thúc bằng burn_scan_done, lượt sau làm tiếp", scanBatch)
		}
	}
	detail := strings.TrimSpace(in.Detail)
	if msg, ok, err := s.fold(ctx, items, strings.TrimSpace(in.Item), title, kind, detail, priorities[strings.TrimSpace(in.Priority)]); ok || err != nil {
		return msg, err
	}
	if n > 0 && why != "" { // its own earlier fail, not the run's general lessons (Reflexion, arXiv:2303.11366)
		detail = fmt.Sprintf("Việc này đã fail trước vì: %s\n\n%s", oneLine(why, 300), detail)
	}
	it, err := s.store.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: oneLine(title, 160), Kind: kind, Detail: detail,
		Priority: priorities[strings.TrimSpace(in.Priority)], RunBranch: b.RunBranch})
	if err != nil {
		return "", err
	}
	if from != "" {
		s.mu.Lock()
		s.found[from]++
		s.mu.Unlock()
	}
	s.wake(b.ProjectID) // a free slot does it
	return "Đã ghi việc " + it.ID + ".", nil
}

// maxFolded: findings one piece holds at most (its own and those folded in),
// so a piece stays one worker's turn.
const maxFolded = 5

// foldMark starts a finding folded into a piece's brief.
const foldMark = "\n\n---\nGộp thêm: "

// fold adds a finding to a piece still waiting (status found) instead of
// recording a new one: into names it (one kind of flaw in several places),
// or, with none named, a waiting piece of the same kind on the same files
// whose title says much the same (the same finding again, worded otherwise).
// ok: folded, msg says where.
func (s *Service) fold(ctx context.Context, items []storage.BurnItem, into, title, kind, detail string, prio int) (msg string, ok bool, err error) {
	var target storage.BurnItem
	if into != "" {
		for _, it := range items {
			if it.ID == into {
				target = it
			}
		}
		switch {
		case target.ID == "" || hunting(target):
			return "", false, fmt.Errorf("không có việc %q để gộp vào", into)
		case target.Status != "found" || target.Kind == KindQuest:
			return "", false, fmt.Errorf("việc %s đã %s, chỉ gộp được vào việc đang chờ (found): ghi việc mới, bỏ item", into, target.Status)
		case folded(target) >= maxFolded:
			return "", false, fmt.Errorf("việc %s đã gộp đủ %d phát hiện: ghi việc mới, bỏ item", into, maxFolded)
		}
	} else {
		mine := paths(storage.BurnItem{Title: title, Detail: detail})
		if len(mine) == 0 {
			return "", false, nil
		}
		all := [][]string{mine}
		for _, it := range items {
			all = append(all, paths(it))
		}
		amb := basenameDirs(all...)
		for _, it := range items {
			if it.Status == "found" && it.Kind == kind && !hunting(it) && folded(it) < maxFolded && overlapStrict(mine, paths(it), amb) && alike(title, it.Title) {
				target = it
				break
			}
		}
		if target.ID == "" {
			return "", false, nil
		}
	}
	target.Detail = strings.TrimSpace(target.Detail) + foldMark + oneLine(title, 160) + "\n" + detail
	if r := []rune(target.Detail); len(r) > maxQuestDetail*3 {
		return "", false, nil // too long to hold another: a piece of its own
	}
	target.Priority = max(target.Priority, prio)
	if err := s.store.Burn().UpdateItemFrom(ctx, target, "found"); err != nil {
		if errors.Is(err, storage.ErrConflict) && into == "" {
			return "", false, nil // taken meanwhile: a piece of its own
		}
		return "", false, err
	}
	return fmt.Sprintf("Đã gộp vào việc %s (%s), cùng loại và cùng chỗ: không ghi việc mới.", target.ID, oneLine(target.Title, 80)), true, nil
}

// alike: two titles share most of their words (diacritics and case aside,
// over the shorter one's words): the same finding, worded otherwise.
func alike(a, b string) bool {
	words := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.FieldsFunc(textrank.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			out[w] = true
		}
		return out
	}
	x, y := words(a), words(b)
	if len(x) > len(y) {
		x, y = y, x
	}
	if len(x) < 3 {
		return false
	}
	n := 0
	for w := range x {
		if y[w] {
			n++
		}
	}
	return n*5 >= len(x)*3 // at least 60%
}

// folded: how many findings a piece holds.
func folded(it storage.BurnItem) int { return 1 + strings.Count(it.Detail, foldMark) }

// scanDone ends a scan: what it looked at and its code map are kept, the
// scan dropped (work, once the turn ends, lets go of its worktree).
func (s *Service) scanDone(ctx context.Context, b storage.BurnSession, it storage.BurnItem, in ToolInput) (string, error) {
	if !hunting(it) {
		return "", fmt.Errorf("việc %s không phải lượt quét: báo burn_done hoặc burn_fail", it.ID)
	}
	plan := strings.TrimSpace(in.Coverage)
	if plan != "" {
		before := b.Coverage
		if cur, err := s.store.Burn().SessionByID(ctx, b.ID); err == nil {
			before = cur.Coverage
		}
		plan = mergeCoverage(before, plan)
		if why := planProblem(before, plan); why != "" {
			return "", errors.New("coverage chưa nhận: " + why + ". Sửa rồi gọi lại burn_scan_done")
		}
		var fits bool
		if plan, fits = fitCoverage(tagged(plan, templateOf(b))); !fits {
			// before (already saved) may itself be past maxCoverage, debt from
			// earlier scans: a plan that does not grow it further is still
			// accepted (ADR-141 would otherwise jam burn_scan_done with no way
			// out once debt exists, since marking an existing item [x] never
			// makes the plan shorter than before — "- [ ] " and "- [x] " are
			// the same length). A plan that adds a new area, or reopens one, is
			// still refused.
			wasLen := len([]rune(tagged(before, templateOf(b))))
			if len([]rune(plan)) > wasLen {
				return "", fmt.Errorf("coverage chưa nhận: kế hoạch dài %d ký tự, tối đa %d (đã %d ký tự từ trước lượt này, nợ tích lũy không phải lỗi lượt này); kể cả khi đã rút gọn kết quả các mục [x]: đánh dấu \"- [x] vùng — trùng mục <vùng đã có>\" cho mục thật sự trùng để rút ngắn, hoặc đừng thêm mục mới, rồi gọi lại burn_scan_done", len([]rune(plan)), maxCoverage, wasLen)
			}
		}
	}
	s.keepScanned(ctx, b, in.Scanned)
	if m := strings.TrimSpace(in.Map); m != "" {
		if r := []rune(m); len(r) > maxCodeMap {
			m = string(r[:maxCodeMap])
		}
		_ = s.store.Burn().SetCodeMap(context.WithoutCancel(ctx), b.ID, m)
	}
	s.keepLessons(ctx, b, in.Lessons)
	s.keepCoverage(ctx, b, plan)
	if err := s.store.Burn().DeleteItem(ctx, it.ID); err != nil {
		return "", err
	}
	n := s.endScan(ctx, b, it.ID)
	return fmt.Sprintf("Đã kết thúc lượt quét (%d việc). Dừng ở đây.", n), nil
}

// startScan notes how much of the coverage plan was checked when scan
// started, to tell whether it moved it on.
func (s *Service) startScan(b storage.BurnSession, scan string) {
	checked, _ := coverageCount(b.Coverage)
	s.mu.Lock()
	s.marked[scan] = checked
	s.mu.Unlock()
}

// endScan counts what scan did: a piece recorded or a coverage item checked
// moves the run on (the count of scans in a row with neither starts again);
// every item checked and nothing found, scans are over (ADR-141). It says
// so in the Burn's chat.
func (s *Service) endScan(ctx context.Context, b storage.BurnSession, scan string) int {
	plan := b.Coverage
	if cur, err := s.store.Burn().SessionByID(context.WithoutCancel(ctx), b.ID); err == nil {
		plan = cur.Coverage
	}
	checked, total := coverageCount(plan)
	s.mu.Lock()
	n := s.found[scan]
	before, ok := s.marked[scan]
	if !ok { // a scan from before a restart
		before = checked
	}
	delete(s.found, scan)
	delete(s.marked, scan)
	moved := n > 0 || checked > before
	if moved {
		delete(s.nones, runKey(b))
	} else {
		s.nones[runKey(b)]++
	}
	stuck := s.nones[runKey(b)] >= maxNones
	done := total > 0 && checked == total && n == 0
	if done {
		s.full[runKey(b)] = true
	}
	s.mu.Unlock()
	cov := ""
	if total > 0 {
		cov = fmt.Sprintf(" · đã phủ %d/%d mục", checked, total)
	}
	switch {
	case n > 0:
		s.say(ctx, b, fmt.Sprintf("**Quét xong**: %d việc mới trong Tìm thấy%s", n, cov))
	case done:
		s.say(ctx, b, fmt.Sprintf("**Đã phủ hết** %d mục: không còn việc đáng làm, ngưng quét.", total))
	case stuck && total > checked:
		s.say(ctx, b, fmt.Sprintf("%d lượt quét liền không tiến triển, còn %d/%d mục chưa phủ: ngưng quét (bấm Tiếp tục để quét lại).", maxNones, total-checked, total))
	case moved:
		s.say(ctx, b, "Lượt quét không ghi việc mới"+cov+".")
	default:
		s.say(ctx, b, "Lượt quét không tìm thấy việc đáng làm"+cov+".")
	}
	s.wake(b.ProjectID)
	return n
}

// scansOver: every coverage item checked with nothing more found, or
// maxNones scans in a row moved nothing on, this run.
func (s *Service) scansOver(b storage.BurnSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.full[runKey(b)] || s.nones[runKey(b)] >= maxNones
}

// keepCoverage saves a scan's coverage plan, whole (ADR-141): scanDone is
// the only caller, and it already decided the plan is acceptable (fits, or
// does not grow past-cap debt further), so no item is dropped here.
func (s *Service) keepCoverage(ctx context.Context, b storage.BurnSession, plan string) {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return
	}
	plan, _ = fitCoverage(tagged(plan, templateOf(b)))
	_ = s.store.Burn().SetCoverage(context.WithoutCancel(ctx), b.ID, plan)
}

// keepScanned adds what a scan looked at to the Burn's areas scanned.
func (s *Service) keepScanned(ctx context.Context, b storage.BurnSession, area string) {
	area = oneLine(strings.TrimSpace(area), 400)
	if area == "" {
		return
	}
	s.mu.Lock() // one at a time, none lost
	defer s.mu.Unlock()
	cur, err := s.store.Burn().SessionByID(ctx, b.ID)
	if err != nil {
		return
	}
	_ = s.store.Burn().SetScanned(context.WithoutCancel(ctx), b.ID, addScanned(cur.Scanned, area, time.Now()))
}

// sameTitle: two titles that say the same, spacing and case aside.
func sameTitle(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}
