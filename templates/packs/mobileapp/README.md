# Ứng dụng di động

Repo phát triển một ứng dụng di động (iOS, Android), chạy bằng agent trên agent-office, dựng từ mẫu `@agentoffice/mobileapp`. Code của ứng dụng nằm ngay trong repo này.

## Luồng

```
yêu cầu ──► spec có tiêu chí chấp nhận theo nền tảng (bạn duyệt)
                 │
                 ▼
     làm tính năng: test trước ──► soát diff + kiểm trên simulator/emulator ──► FAIL thì làm lại
                 │                                                               │
                 ▼                                                               ▼
            lỗi: test tái hiện ──► sửa đúng nguyên nhân ──► kiểm lại            PASS
                                                                                 │
                                                                                 ▼
        kiểm tra trước khi lên store (phiên bản, build, test, quyền, nhãn quyền riêng tư, ảnh store)
                                                                                 │
                                                                                 ▼
                                   bạn ký và tải lên TestFlight / Google Play
```

| Bước | Làm thế nào | Ai |
|---|---|---|
| Yêu cầu | "Thêm đăng nhập bằng Apple", "app mở chậm trên Android" | Trưởng dự án ghi vào `features` / `bugs` |
| Spec | `#feature-spec`: màn hình, luồng, tiêu chí chấp nhận, quyền, offline | Trưởng dự án viết, Soát và kiểm thử phản biện, bạn duyệt |
| Làm | `#build-feature`: test trước, typecheck/lint/test, build từng nền tảng | Lập trình mobile làm, Soát và kiểm thử nghiệm thu |
| Sửa lỗi | `#fix-bug`: test tái hiện, sửa nguyên nhân, kiểm lại trên đúng nền tảng | Lập trình mobile, Soát và kiểm thử |
| Lên store | `#release-check`: PASS/FAIL có bằng chứng, ghi vào `releases`, danh sách việc bạn làm khi tải lên | Soát và kiểm thử |

## Agent
- **Trưởng dự án mobile** (mặc định): nói chuyện với bạn, giữ mô tả sản phẩm, backlog và hồ sơ store, viết spec, điều phối.
- **Lập trình mobile**: làm màn hình, điều hướng, dữ liệu, API theo test trước.
- **Soát và kiểm thử mobile**: soát diff, chạy test, kiểm trên simulator/emulator, quyền riêng tư, a11y. Nên gắn kết nối AI khác hãng với Lập trình mobile.

## Công cụ cần có trên máy
| Công cụ | Dùng cho | Ghi chú |
|---|---|---|
| Xcode + iOS Simulator | build và chạy iOS | chỉ có trên macOS |
| Android Studio + emulator, `adb` | build và chạy Android | |
| Node + Expo CLI / Flutter SDK | theo framework trong `docs/stack.md` | |
| Maestro hoặc Detox | luồng UI tự động | không bắt buộc, nên có |
| context7 MCP | đọc tài liệu mới nhất của thư viện, SDK | kết nối ở cài đặt MCP của office |
| Playwright MCP | kiểm bản Expo web (nếu có) | không thay được kiểm trên máy |

Skill trong thư viện office (tìm bằng find_skills): `design-polish`, `launch-checklist`, `research-brief`. Agent không tự cài gì; thiếu công cụ thì agent nói rõ phần nào chưa kiểm được.

## Dữ liệu
- `features`: backlog, nền tảng, ưu tiên, trạng thái, spec, tiêu chí chấp nhận.
- `bugs`: bước tái hiện, thiết bị, hệ điều hành, phiên bản app, mức độ, nguyên nhân.
- `releases`: mỗi lần kiểm tra trước khi lên store, PASS/FAIL, báo cáo, kênh đã tải lên.
- `decisions`: quyết định khó đảo ngược (framework, backend, push, SDK trả phí).

## Thư mục
- `docs/product.md`: ứng dụng là gì, cho ai, nền tảng, luồng chính, phạm vi.
- `docs/stack.md`: công nghệ, lệnh chạy/test/build, tên biến môi trường và khóa (không ghi giá trị).
- `docs/store.md`: thông tin, ảnh và nhãn quyền riêng tư để lên store.
- `docs/specs/`: mỗi spec một file.

## Lịch (tắt sẵn)
- **Kiểm tra sức khỏe hằng ngày** (9:00 thứ 2-6): typecheck, lint, test, build debug; lỗi thì mở bug.
- **Soát thư viện và SDK hằng tuần** (10:00 thứ 2): audit thư viện, SDK cũ, hạn mới của store.
- **Kiểm tra trước khi lên store hằng tuần** (16:00 thứ 6): chạy `#release-check`.

Bật ở trang Tự động hóa của project khi đã có code và lệnh trong `docs/stack.md`.

## Bắt đầu
1. Mở chat với Trưởng dự án mobile, kể ứng dụng làm gì, cho ai, nền tảng nào; agent điền `docs/product.md`.
2. Repo trống: agent đề xuất công nghệ (mặc định Expo), bạn đồng ý rồi agent dựng khung và ghi lệnh vào `docs/stack.md`. Repo có sẵn code: agent đọc và điền `docs/stack.md`.
3. Cài Xcode / Android Studio nếu chưa có, kết nối context7 MCP.
4. Đưa tính năng đầu tiên, duyệt spec, để `#build-feature` làm.

## Giới hạn
Không tiêu tiền, không đăng ký tài khoản developer hay SDK trả phí, không đưa bí mật hay khóa ký vào repo hoặc chat. Agent không ký, không tải lên, không gửi duyệt lên App Store hay Google Play: bạn làm bước đó. Push lên nhánh chính luôn chờ bạn đồng ý.
