# agent-office (backend Go)

Kiến trúc đã có trong `docs/ARCHITECTURE.md`, `docs/CLI.md`, `docs/DATA_MODEL.md`. File này chỉ ghi lệnh và quy ước agent không tự tìm được khi đọc repo.

## Commands

- Build: `go build -o bin/office ./cmd/office`
- Vet + test toàn repo: `go vet ./... && go test ./...` (dùng trong `make test`)
- Test 1 package: `go test ./internal/<pkg>/...`
- Test 1 hàm: `go test ./internal/<pkg>/... -run TestTenHam`
- Race + lặp (việc liên quan khoá/CAS): `go test ./internal/<pkg>/... -race -count=3`
- Format: `gofmt -l .` (danh sách trống nghĩa là sạch)
- Test mới: mặc định `t.Parallel()`; khi nào không được, xem `docs/TESTING.md`
- Dashboard (Nuxt): xem `dashboard/CLAUDE.md`

## Conventions

- `_test.go` thường; file kết thúc `_internal_test.go` test qua package nội bộ (không qua API public) — theo đúng tên file đang có trong package đó, đừng đổi khi không cần.
- `.office/worktrees/` là bản sao tạm của mỗi Burn/chat, không sửa trực tiếp; thay đổi thật nằm ở nhánh/project gốc.
- CAS (compare-and-swap) đặt ở tầng storage (`UPDATE … WHERE version/updated_at`), không ở tầng service; lỗi "dữ liệu cũ" (`ErrStale`) phải tách khỏi lỗi "trùng tên" (`ErrConflict`).
- Cắt chuỗi có tiếng Việt: cắt theo rune hoặc theo biên UTF-8, không dùng `s[:n]` theo byte.
- Kiểm quyền sở hữu project ngay sau khi tải đối tượng (mẫu `checkRun`), không chỉ dựa vào role.

## Gotchas

- Role `admin` (storage.RoleAdmin) là quyền toàn office, không theo project — endpoint gate bằng `requireRole(RoleAdmin)` không cần kiểm thêm `project_id`.
- Trần ngân sách (usage) là trần mềm theo thiết kế: chạm trần thì chặn lượt mới, không phải lỗi race cần sửa.
