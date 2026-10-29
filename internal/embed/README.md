# internal/embed

Tìm ghi chú của agent theo nghĩa không cần service riêng (ADR-236): embedding từ endpoint `/embeddings` tương thích OpenAI (kết nối OpenAI, tương thích OpenAI như Ollama, hoặc `OFFICE_EMBED_URL`), lưu trong setting `embed_index/<project>`; `recall` so cosine. Bật/tắt bằng `office embed`.

- Phụ thuộc: storage
