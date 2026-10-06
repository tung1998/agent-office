# migrations

Migration goose, nhúng vào binary qua `embed.go`. Mỗi dialect một thư mục, cùng số version (ADR-003).

- `sqlite/`: dùng cho chế độ local.
- `postgres/`: thêm khi làm driver Postgres.

Thêm migration: tạo file `000NN_<tên>.sql` với khối `-- +goose Up` / `-- +goose Down` trong cả hai thư mục.
