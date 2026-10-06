# Dashboard (Nuxt 4 + Nuxt UI)

## i18n: bắt buộc
- Không viết chữ hiển thị thẳng trong `.vue`/`.ts`. Dùng `const { t, dateLocale } = useLang()` và `t('khu.vuc.key', { n })`.
- Key nằm trong `app/locales/parts/<khu>.vi.ts` (tiếng Việt, nguồn của key) và `<khu>.en.ts` (tiếng Anh; TypeScript báo lỗi nếu thiếu key). Chuỗi dùng chung: `common.*`, tên menu/tab: `nav.*` trong `app/locales/vi.ts`/`en.ts`.
- Map/hằng chứa chữ phải tính lúc render (computed, hàm, getter), không gọi `t()` một lần lúc khởi tạo.
- Ngày giờ: `toLocale*String(dateLocale.value, …)`, không cứng `'vi-VN'`.
- Chữ từ backend (lỗi, output AI) giữ nguyên; enum từ backend (status, kind, level) thì dịch theo key.
- Kiểm tra: `node scripts/check-i18n.mjs` (chạy trong `make test`, `make ui-build` và "Cập nhật office" khi bật test). Dòng buộc phải giữ tiếng Việt (prompt gửi AI, regex) thêm `// i18n-ignore`.
