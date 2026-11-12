# Test backend: tốc độ và quy ước

`go test ./...` chạy các package song song, nên thời gian chờ ≈ package chậm nhất.
Đo lại (11/2026): 1m44s → ~48s sau khi cho test độc lập chạy `t.Parallel()`.

| Package | Trước | Sau |
|---|---|---|
| internal/chat | 97s | ~41s |
| internal/burn | 49s | ~15s |
| internal/api | 39s | ~16–25s |
| internal/fullexport | 22s | ~13s |

## Viết test mới

- Mặc định gọi `t.Parallel()` ở dòng đầu, **trừ** khi test (hoặc helper nó gọi):
  - dùng `t.Setenv`/`os.Setenv`/`os.Chdir` (Go panic nếu song song);
  - sửa biến cấp package (`chat.QuotaRetrySlack`, `mergeWaitPoll`, `GuardCommand`, `testUpdater`, `cliPath`, `rgPath`, `homeDir`, `OfficeDir`, `SetBeforePatchSave`…; mẫu `old := x; x = …; defer/Cleanup restore`);
  - dùng trạng thái chung của tiến trình: `cliuse` (CLI đang chạy), khoá xuất/nhập (`fullexport`, export/import project, paired office), `clitools` API.
- Không song song cả package: `internal/clitools`, `internal/mcpgateway`, `cmd/office` (dùng `cliuse`/biến toàn cục của stdio; thử song song thì lỗi hoặc race).
- Chờ điều kiện bằng vòng poll ngắn (`until`, `waitFor`), không `time.Sleep` cố định. Hằng số thời gian thật (backoff, quota, heartbeat) cho test chỉnh qua biến package — test đó phải chạy tuần tự.

## Kiểm sau khi thêm `t.Parallel()`

```sh
go test -race -count=2 ./internal/<pkg>/
```

## Tìm test chậm

```sh
go test -count=1 -json ./internal/<pkg>/ \
 | jq -r 'select(.Action=="pass" and .Test!=null and (.Test|contains("/")|not)) | "\(.Elapsed)\t\(.Test)"' \
 | sort -rn | head
```

Test chậm nhất còn lại chủ yếu chờ thời gian thật có chủ đích (quota chờ reset, vòng workflow `wait 1s`, CLI giả `sleep`, heartbeat office): `TestResumedLoopRunsStepsAnew`, `TestQuotaRetrySurvivesRestart`, `TestQuotaGivesUp`, `TestBurnScanRecordsThenWorkersDo`, `TestToolsUpdateOfficeDiedAfterClaim`. Muốn nhanh hơn nữa: đưa thời lượng đó thành biến chỉnh được trong test, không xoá test.
