# Phát triển ứng dụng web

Repo phát triển một ứng dụng web, chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/webapp`. Code của ứng dụng nằm ngay trong repo này.

## Luồng

```
yêu cầu ──► spec có tiêu chí chấp nhận (bạn duyệt)
                 │
                 ▼
     làm tính năng: test trước ──► soát diff + kiểm trên trình duyệt ──► FAIL thì làm lại
                 │                                                        │
                 ▼                                                        ▼
            lỗi: test tái hiện ──► sửa đúng nguyên nhân ──► kiểm lại     PASS
                                                                          │
                                                                          ▼
                     kiểm tra trước phát hành (build, test, audit, trình duyệt, a11y, hiệu năng)
                                                                          │
                                                                          ▼
                                                   triển khai: chỉ khi bạn đồng ý
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Yêu cầu | "Thêm trang đăng nhập bằng Google", "trang sản phẩm chậm quá" | Trưởng dự án ghi vào `features` / `bugs` |
| Spec | `#feature-spec`: vấn đề, luồng, tiêu chí chấp nhận, ngoài phạm vi | Trưởng dự án viết, Soát và kiểm thử phản biện, bạn duyệt |
| Làm | `#build-feature`: test trước, chạy build/typecheck/lint/test | Lập trình web làm, Soát và kiểm thử nghiệm thu |
| Sửa lỗi | `#fix-bug`: test tái hiện, sửa nguyên nhân, kiểm lại | Lập trình web, Soát và kiểm thử |
| Phát hành | `#release-check`: PASS/FAIL có bằng chứng, ghi vào `releases` | Soát và kiểm thử |

## Agent
- **Trưởng dự án web** (mặc định): nói chuyện với bạn, giữ mô tả sản phẩm và backlog, viết spec, điều phối, báo tiến độ.
- **Lập trình web**: làm full-stack (giao diện, API, dữ liệu) theo test trước.
- **Soát và kiểm thử**: soát diff, chạy test, kiểm trên trình duyệt thật, bảo mật, a11y. Nên gắn kết nối AI khác hãng với Lập trình web.

## Công cụ nên kết nối
- **Playwright MCP**: để agent mở ứng dụng trên trình duyệt thật, bấm, chụp màn hình, đọc console. Thiếu thì việc kiểm trên trình duyệt bị bỏ qua (agent sẽ nói rõ).
- **context7 MCP**: đọc tài liệu mới nhất của thư viện, framework thay vì đoán.
- Skill trong thư viện office (tìm bằng find_skills): `website-blueprint`, `page-builder`, `design-polish`, `launch-checklist`.

Kết nối MCP ở phần cài đặt MCP của office; agent không tự cài gì.

## Dữ liệu
- `features`: backlog, ưu tiên, trạng thái, spec, tiêu chí chấp nhận.
- `bugs`: bước tái hiện, mong đợi, thực tế, mức độ, nguyên nhân.
- `releases`: mỗi lần kiểm tra trước phát hành, PASS/FAIL, báo cáo.
- `decisions`: quyết định khó đảo ngược (framework, cơ sở dữ liệu, hosting, dịch vụ trả phí).

## Thư mục
- `docs/product.md`: ứng dụng là gì, cho ai, luồng chính, phạm vi.
- `docs/stack.md`: công nghệ, lệnh build/test/chạy/triển khai, tên biến môi trường.
- `docs/specs/`: mỗi spec một file.

## Lịch (tắt sẵn)
- **Kiểm tra sức khỏe hằng ngày** (9:00 thứ 2-6): build, typecheck, lint, test; lỗi thì mở bug.
- **Soát bảo mật thư viện hằng tuần** (10:00 thứ 2): audit thư viện, báo lỗ hổng cao.
- **Kiểm tra trước phát hành hằng tuần** (16:00 thứ 6): chạy `#release-check`.

Bật ở trang Tự động hóa của project khi đã có code và lệnh trong `docs/stack.md`.

## Bắt đầu
1. Mở chat với Trưởng dự án web, kể ứng dụng làm gì, cho ai; agent điền `docs/product.md`.
2. Repo trống: agent đề xuất công nghệ, bạn đồng ý rồi agent dựng khung và ghi lệnh vào `docs/stack.md`. Repo có sẵn code: agent đọc và điền `docs/stack.md`.
3. Kết nối Playwright MCP và context7 MCP.
4. Đưa tính năng đầu tiên, duyệt spec, để `#build-feature` làm.

## Giới hạn
Không tiêu tiền, không đăng ký dịch vụ, không đưa bí mật vào repo hay chat. Push lên nhánh chính và triển khai production luôn chờ bạn đồng ý.
