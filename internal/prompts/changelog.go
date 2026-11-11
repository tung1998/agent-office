package prompts

// ChangeEntry is one recorded edit of a prompt: the content hash (Version)
// it produced, when, and what changed. Entries only ever append — the
// current entry for a name must match its live Version (enforced by
// TestChangelogCoversCurrentVersion), so editing a .md without adding an
// entry here fails the build instead of silently losing the "what changed"
// note.
type ChangeEntry struct {
	Hash, Date, Note string
}

// Changelog returns the recorded edit history for every prompt that has
// one. A name absent from it has never been through this changelog (added
// after the bootstrap, or not yet covered).
func Changelog() map[string][]ChangeEntry {
	return changelog
}

// UnrecordedVersions lists prompts whose current Version has no matching
// entry in Changelog — a .md was edited (or added) without a changelog
// entry describing the change.
func UnrecordedVersions() []string {
	var missing []string
	for _, n := range Names() {
		v := Version(n)
		found := false
		for _, e := range changelog[n] {
			if e.Hash == v {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, n)
		}
	}
	return missing
}

var changelog = map[string][]ChangeEntry{
	"advice/done":            {{Hash: "d47bbb8edd5d", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"advice/later":           {{Hash: "e4cd45ca9e6b", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"advice/no-advisor":      {{Hash: "fcfaf3e07070", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"advice/note":            {{Hash: "80e6863d15db", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"advice/repeat":          {{Hash: "3abf90b2ffbc", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"assistant/instructions": {{Hash: "2edf7db4adfb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"bot/scope-filter":       {{Hash: "f5a23da400b2", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"burn/hunt-builder":      {{Hash: "691c5d976010", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}, {Hash: "38a044e8be68", Date: "2026-11-07", Note: "AGENTS.md có mục ## Checks và ## Run để office tự kiểm chứng và chạy preview (ADR-266)"}, {Hash: "35d13ee88cbe", Date: "2026-11-07", Note: "repo đã có code: việc đầu là Nhận dự án đang có (SPEC từ code/docs, tính năng đã chạy được đánh passes sau khi chạy acceptance, không dựng khung) (ADR-266)"}},
	"burn/hunt-ideas":        {{Hash: "d8612f45c933", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"burn/hunt-ux":           {{Hash: "e8137a9950b4", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"burn/report":            {{Hash: "12acda1f08b2", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"burn/retro": {
		{Hash: "bb965588cf6c", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"},
		{Hash: "3deab392ad24", Date: "2026-11-07", Note: "thêm [[.PromptVersion]] vào dòng mở đầu, để retro ghi lại nó đã chạy bằng bản prompt nào"},
	},
	"burn/review": {{Hash: "08995d32ccd9", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"burn/scan": {
		{Hash: "4cebc67f705a", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"},
		{Hash: "d65a3df2ddae", Date: "2026-11-07", Note: "gộp các phát hiện cùng loại vào một piece (fold findings)"},
	},
	"burn/work":                   {{Hash: "a1d274d1995c", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/admin-instructions":     {{Hash: "5564b9560dcb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/administrator":          {{Hash: "6a9c190b2938", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/answer-only":            {{Hash: "be39635de506", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/api-resumed":            {{Hash: "524c83953b53", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/api-retry":              {{Hash: "7d3752339c39", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/api-retry-over":         {{Hash: "7d3be5139b52", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/api-retry-planned":      {{Hash: "bf4ba1a5c518", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/assistant-power":        {{Hash: "5045cc6f4ae3", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/command-call":           {{Hash: "d783601d292d", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/conflicts":              {{Hash: "511b85ed830e", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/consult":                {{Hash: "a3cc84fadefc", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/decided":                {{Hash: "75bda9948f59", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/diff":                   {{Hash: "77e23010254b", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/edit-direct":            {{Hash: "2ef45cee5073", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/edit-worktree":          {{Hash: "b1bf28ba28da", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/experts":                {{Hash: "d721eebe9ce5", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/intro":                  {{Hash: "540634a9dac2", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/language":               {{Hash: "9bd0bbb3a998", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/linked":                 {{Hash: "659926b23dc7", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/linked-report":          {{Hash: "c5ebc0409edb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/mcp-broken":             {{Hash: "9fba6997765d", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/mcp-others":             {{Hash: "782e7bf89cb3", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/mcp-toolbox":            {{Hash: "29413c13bb9e", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/office-tools":           {{Hash: "823da31128e0", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/pack-sync":              {{Hash: "b730828dcd04", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/read-only":              {{Hash: "17a807e686e9", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/read-worktree":          {{Hash: "da2b99a0a119", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/resume-over":            {{Hash: "110095d75a63", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/resumed":                {{Hash: "1eb637b893a1", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/role-changed":           {{Hash: "265f8d8c1bba", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/rules":                  {{Hash: "430f5198ebcb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/skill-call":             {{Hash: "da35904e60c5", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/team":                   {{Hash: "e21c2a1c4ddc", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/workflows":              {{Hash: "350ee98b97cb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/worktree":               {{Hash: "6e8f4350f5d6", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"guide/automation":            {{Hash: "19e5c53a589c", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"guide/skill":                 {{Hash: "05d484ddf150", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"guide/skill-handoff":         {{Hash: "6ad0b72d2fb5", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"guide/workflow":              {{Hash: "1ecbdc67a3d9", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/asked":               {{Hash: "67bc00ac7bca", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/checks":              {{Hash: "1ad3fae609b4", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/consult":             {{Hash: "65a12d9fbdf3", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/expert-answer":       {{Hash: "e0f5280d0126", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/handed":              {{Hash: "a36360f091cc", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/next":                {{Hash: "5f0d40927aef", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"handoff/task":                {{Hash: "808e144875f7", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"heartbeat/stuck":             {{Hash: "579651ea594f", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"memory/compact":              {{Hash: "9e2a8babfa57", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"memory/notes":                {{Hash: "2e1337ad415c", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"memory/review":               {{Hash: "fe022803c680", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"monitor/analyse":             {{Hash: "77aae2408f1e", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"setup/language":              {{Hash: "ed9acfedb5c4", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"setup/project":               {{Hash: "e1b5deab3687", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"setup/schema":                {{Hash: "cfd90a4ed5d5", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"setup/system":                {{Hash: "61a1f2179b75", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"skills/own":                  {{Hash: "68ed2ba1fce4", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"skills/use":                  {{Hash: "3b10383ec9d0", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/api-resumed":         {{Hash: "c89d1d06e585", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/bot-api-wait":        {{Hash: "1db26eb0a9c5", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/bot-message":         {{Hash: "6173cbe044e9", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/goal-again":          {{Hash: "3991a079627e", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/goal-judge":          {{Hash: "32817fb04a80", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/goal-reflect":        {{Hash: "4204358e2e4b", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/payload":             {{Hash: "ba6e7d62633b", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"trigger/resumed":             {{Hash: "4c338b257bda", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/access-note":        {{Hash: "1c883ae09664", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/ask":                {{Hash: "5efca879fc13", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/brief":              {{Hash: "5d27b50dc91a", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/callback":           {{Hash: "e76c3c61e410", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/coordinator":        {{Hash: "ad1d143750cb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/role":               {{Hash: "8f35f57aa4fb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/send":               {{Hash: "a0477ecdacb1", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/start":              {{Hash: "1a15b77d9b67", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/step-api-resumed":   {{Hash: "57f025f81d1b", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/step-api-wait":      {{Hash: "a4011653afbb", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/step-resumed":       {{Hash: "11906352689d", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/supervise":          {{Hash: "3f8c84da823a", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/supervisor":         {{Hash: "9d7f8be715d8", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"workflow/vote":               {{Hash: "d0875d35666d", Date: "2026-11-07", Note: "khởi tạo: ghi mã phiên bản ban đầu"}},
	"chat/quota-retry-over":       {{Hash: "763cb8fdc0b9", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"chat/quota-retry-planned":    {{Hash: "f9cbb56e9904", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"chat/quota-retry":            {{Hash: "07ffe5f0d71a", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"trigger/bot-quota-wait":      {{Hash: "76ecb33a4351", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"workflow/step-quota-resumed": {{Hash: "5dfc9038fd64", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"workflow/step-quota-wait":    {{Hash: "4b2599c83036", Date: "2026-11-08", Note: "mới: hết quota thì chờ reset rồi tự chạy tiếp (ADR-269)"}},
	"chat/btw":                    {{Hash: "946847d46ee2", Date: "2026-11-11", Note: "mới: /btw hỏi chen, trả lời riêng chỉ đọc (ADR-272)"}},
	"chat/goal-start":             {{Hash: "6fc53a1c56b5", Date: "2026-11-11", Note: "mới: /goal trong chat, làm tới khi đạt (ADR-272)"}},
}
