# Golden set: bộ việc mẫu cố định để so trước/sau khi đổi prompt/model

Nguồn gốc: `docs/research/agent-eval-mcp.md` (mục Eval) — office có cost mỗi run
(`internal/burn/report.go`, field `BurnItem.CostUSD`) và review 3 tầng
(`internal/burn/review.go:24`), nhưng không có "accuracy" trên một bộ việc cố
định để so hai lần chạy khác prompt/model/workflow. File này là bộ việc đó:
tĩnh, không đổi theo thời gian — khi một bit ở đây không còn khớp code (bị xoá,
đổi tên), sửa lại rubric, đừng xoá cả dòng, để lịch sử so sánh cũ còn đọc được.

6 bit Burn đã `done`, chọn đại diện 3 loại (bug, idea, quest) và nhiều mức khó:

| # | Bit gốc | Loại | Đề bài | Tiêu chí đạt (rubric) |
|---|---|---|---|---|
| G1 | `bit_01m4nap401hga51c2v3kxxw7bp` | bug (bảo mật) | `CheckExtraDir`/`DenyPaths` (`internal/perm/extradir.go`) so sánh đường dẫn kiểu case-sensitive trên macOS (filesystem case-insensitive): một extra-dir hoặc deny-path viết hoa/thường khác `~/.ssh`, `~/.aws`, `.office`, `.env` vẫn lọt qua, để lộ thư mục bí mật. | Đạt nếu bản vá dùng so sánh không phân biệt hoa/thường (`strings.EqualFold` hoặc tương đương) cho mọi so sánh tên thư mục nhạy cảm trong `CheckExtraDir` và đường dẫn deny, **và** có test tái hiện bằng một biến thể hoa/thường (vd `.SSH`, `.Office`) chứng minh bị chặn. Không đạt nếu chỉ sửa 1 trong các nhánh (vd `.ssh` nhưng bỏ sót `.office`) hoặc không có test. |
| G2 | `bit_01m4x9c6ygdh0xycjfxmf1f44j` | bug (race/CAS) | `putPolicy` (`internal/perm`) chỉ kiểm version bằng so sánh thường trong code Go (đọc rồi ghi), không phải CAS thật ở tầng storage — hai lượt lưu đồng thời lưu đè nhau mà không báo lỗi. | Đạt nếu CAS chuyển xuống tầng storage qua `Settings().SetIf(ctx, key, expectedRaw, p)` (so sánh chuỗi JSON đã đọc trước đó, không phải so version trong code Go), trả `storage.ErrConflict` khi JSON đã đổi (settings là key-value, không có khái niệm "trùng tên" riêng nên dùng chung `ErrConflict` là đúng, **không** bắt phải có `ErrStale` riêng), **và** có test chạy 2 goroutine lưu đồng thời cùng `expectedRaw` cũ, xác nhận đúng 1 cái thành công (`err == nil`) và cái kia nhận `storage.ErrConflict` (không phải "đôi khi pass"). Không đạt nếu test chỉ gọi tuần tự (không có đụng độ thật) hoặc fix chỉ thêm `sync.Mutex` ở tầng service (không phải CAS ở storage). |
| G3 | `bit_01m4pedk0ypv49v06dd6na1ers` | bug (UI nhỏ) | `events.Followed` (`internal/events/events.go`) thiếu khoá `"burn_review_profiles"` nên trang Review profiles trên dashboard không tự làm mới khi có thay đổi (phải F5 thủ công). | Đạt nếu thêm đúng khoá `"burn_review_profiles": true` vào map `Followed`, **và** xác nhận phía dashboard (`useDataSync.ts` hoặc tương đương) có đăng ký lắng nghe sự kiện này cho trang Review profiles — không chỉ sửa backend mà bỏ qua việc dashboard có thực sự dùng sự kiện đó không. |
| G4 | `bit_01m6rj0bbb3j9vk902j10wsd59` | quest (nghiên cứu) | Viết `docs/research/agent-guides.md`: khảo sát các kho/hướng dẫn học agent còn lại (Prompt Engineering Guide, LLM Course, Made with ML, Awesome Generative AI Guide…), đối chiếu từng ý với office đã có file:dòng nào, thiếu gì, lợi ích nếu làm. | Đạt nếu file có bảng đối chiếu (nguồn → office đã có ở đâu / thiếu gì / lợi ích), **mọi** link hỏng được tự xác nhận bằng cách gọi thử (không đoán), và có tối đa 3 đề xuất cụ thể (hoặc nêu rõ vì sao không đề xuất thêm). Không đạt nếu liệt kê nguồn mà không đối chiếu với code thật (file:dòng), hoặc khẳng định link hỏng mà không kiểm tra. |
| G5 | `bit_01m6s61at9md33sw3bvcc6se9a` | idea (tính năng nhỏ) | Thêm `success_rate` theo từng agent (đã done clean, không reviewAgain, Attempts≤1 chia cho tổng việc đã đóng) cạnh `failure_digest` sẵn có. | Đạt nếu `agentSuccessRate` (`internal/activity/failures.go`) tính đúng công thức trên dữ liệu `BurnItem` thật của project (không gộp pool nhiều agent), tool mới đăng ký đúng chỗ (`internal/activity/tools.go`, `ReadOnly: true`), **và** có test với dữ liệu giả lập ít nhất 2 agent khác tỉ lệ để phân biệt được. Không đạt nếu công thức tính pool chung mọi agent hoặc thiếu test phân biệt agent. |
| G6 | `bit_01m6s61m37ctemjzb1f8k4f7a1` | idea (bảo mật/chính sách) | Khai báo `Risk` (`"low"/"medium"/"high"`) trực tiếp trên từng tool MCP trong `internal/officetools/officetools.go` thay vì chỉ suy ra rủi ro qua regex tên tool (`Risky()`). | Đạt nếu **mọi** tool ghi/thực thi (propose_*, run_command, deps_install, decide_request, burn_assign/drop, workflow_gate...) có `Risk` phù hợp mức tác động thật (ghi dữ liệu người dùng khác, chạy lệnh, duyệt thay người) — không chỉ vài tool dễ thấy — và tool chỉ đọc không bị gắn `Risk: "high"` nhầm. Không đạt nếu bỏ sót một tool ghi/thực thi rõ ràng (vd còn tool nào gọi `propose_` hoặc sửa dữ liệu mà không có `Risk` cao). |

## Cách chạy lại (so sánh trước/sau khi đổi prompt/model/workflow)

1. Đổi thứ cần so (prompt agent, model, hay bước workflow) ở một bản riêng,
   **không** đổi trên bản đang dùng thật.
2. Với mỗi bit G1–G6: `burn_add` một bản sao của đề bài ở cột "Đề bài" (kind
   giữ nguyên bug/idea/quest), giao cho agent/cấu hình cần so sánh chạy trong
   worktree riêng — dùng đúng "Đề bài" ở trên làm detail, không chép thêm gợi ý
   lời giải để không lộ đáp án.
3. Khi agent báo `burn_done`, một agent khác (không phải agent vừa làm) chấm
   theo đúng cột "Tiêu chí đạt": đọc diff thật so với tiêu chí, trả lời đạt/
   không đạt + lý do 1-2 câu — không tự suy diễn ngoài rubric.
4. Gắn cost của lần chạy đó (`BurnItem.CostUSD`, đọc qua `project_report` hoặc
   `failure_digest`/report trong Burn) cạnh kết quả đạt/không đạt.
5. So bảng kết quả hai lần chạy (trước/sau) theo mẫu:

   | Bit | Trước: đạt? | Trước: cost | Sau: đạt? | Sau: cost |
   |---|---|---|---|---|
   | G1 | ... | $... | ... | $... |

   Đổi prompt/model đáng làm khi số bit đạt tăng (hoặc giữ nguyên) **và** cost
   không tăng bất hợp lý — không chỉ nhìn "có vẻ tốt hơn" trên 1-2 việc.

Cảnh giác overfitting (theo đúng video nguồn): khi một thay đổi được chỉnh để
khớp đúng các bit G1–G6, số liệu ở đây không còn đáng tin cho thay đổi tiếp
theo — cần thay bit đó bằng một việc đã `done` khác chưa dùng để tối ưu theo
(`burn_list(what="closed")`).

## Verify: chấm thử rubric trên 3 bit mẫu (G1–G3)

Đây là chấm thử rubric trên lời giải **đã có sẵn** của G1–G3 (không phải chạy
agent từ đầu đến cuối lần này — mục "Cách chạy lại" ở trên mới là quy trình
cho một lần so sánh thật). `CostUSD` của bit gốc có lưu thật trên chính bit
(`internal/burn/burn.go:871` `cur.CostUSD += res.cost`, cột `cost_usd` trong
bảng `burn_items`) — không bị giới hạn 720 giờ như `project_report`/
`failure_digest` (hai tool đó chỉ tổng hợp theo cửa sổ thời gian, không phải
nơi lưu gốc). Đọc trực tiếp từ `.office/office.db`:

```
sqlite3 -readonly -header -column .office/office.db \
  "SELECT id, cost_usd, owner, attempts FROM burn_items WHERE id IN (
   'bit_01m4nap401hga51c2v3kxxw7bp','bit_01m4x9c6ygdh0xycjfxmf1f44j','bit_01m4pedk0ypv49v06dd6na1ers');"
```

Cột `owner` (tên agent làm bit) đang để trống cho cả 3 bit — field này được
office thêm sau, các bit cũ (chạy trước khi có `owner`) không có tên agent ghi
lại; đây là khoảng trống dữ liệu thật, không phải thiếu sót của lần chấm này.
Agent chấm (phiên này, đọc code + chạy test, không chỉnh sửa lời giải cũ):
Burn piece `bit_01m6s983zbb5zvzdsvs7pbbmtg` (vai trò "Thực thi").

| Bit | Agent gốc | Cost gốc | Attempts | Rubric kiểm | Bằng chứng đọc được | Đạt? |
|---|---|---|---|---|---|---|
| G1 | (không ghi, bit cũ chưa có `owner`) | $0.4635 | 1 | so sánh không phân biệt hoa/thường cho **mọi** nhánh nhạy cảm + test biến thể hoa/thường | `internal/perm/extradir.go:88` `strings.EqualFold` cho nhánh `.office`; nhánh `~/.ssh`/`~/.aws`/các `sensitiveHomeDirs` khác đi qua `isAncestorOrSame` (`extradir.go:124-131`) so bằng `os.SameFile` (inode) — tương đương không phân biệt hoa/thường vì dựa trên `os.Stat`, không so chuỗi; test `extradir_test.go:137` `TestCheckExtraDirRejectsCaseVariantSensitiveDir` — `go test ./internal/perm/... -run TestCheckExtraDirRejectsCaseVariantSensitiveDir -v` → PASS | Đạt |
| G2 | (không ghi) | $2.8866 | 3 | CAS ở tầng storage qua `SetIf` so JSON cũ, trả `ErrConflict`, test 2 goroutine đụng độ thật | `internal/perm/perm.go:247` `SavePolicyIf` gọi thẳng `st.Settings().SetIf(ctx, key, expectedRaw, p)` (CAS ở storage, không phải so sánh version trong code Go); `internal/perm/perm_test.go:115` `TestSavePolicyIfConcurrent` cho 2 goroutine cùng gọi `SavePolicyIf` với `raw` cũ, kiểm đúng 1 `err == nil` và 1 `errors.Is(e, storage.ErrConflict)` (`perm_test.go:156`) — khớp rubric đã sửa (không đòi `ErrStale` riêng vì settings không có khái niệm trùng tên) — `go test ./internal/perm/... -run TestSavePolicyIfConcurrent -v` → PASS | Đạt |
| G3 | (không ghi) | $0.2181 | 1 | thêm khoá Followed + dashboard có đăng ký nghe | `internal/events/events.go:20` có `"burn_review_profiles": true`; `dashboard/app/composables/useDataSync.ts:21` map route `/burn/review-profiles` → `['burn_review_profiles']` | Đạt |

Chi phí lần chấm thử này (đọc code + sqlite3 + `go test`, không gọi model
ngoài agent Burn đang viết file): không tách riêng được khỏi cost của cả
piece `bit_01m6s983zbb5zvzdsvs7pbbmtg` (office tính cost theo piece, không
theo từng thao tác trong piece) — ghi nhận ở đây để không lẫn với cost gốc
của G1–G3 ở cột "Cost gốc".

Lệnh lặp lại đúng cách chấm này:
```
go test ./internal/perm/... -run 'TestCheckExtraDirRejectsCaseVariantSensitiveDir|TestSavePolicyIfConcurrent' -v
sqlite3 -readonly -header -column .office/office.db \
  "SELECT id, cost_usd, owner, attempts FROM burn_items WHERE id='<bit_id>';"
```
