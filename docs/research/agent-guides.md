# Kho/hướng dẫn: Prompt Engineering, LLMOps

Nguồn từ link rút gọn lnkd.in (mở bằng fetch, chỉ đọc README/mục lục, không clone).

| Nguồn | Là gì | Nền hay tích hợp được | Office đã có, file:dòng | Thiếu |
|---|---|---|---|---|
| Prompt Engineering Guide (lnkd.in/gJJGbXQr) | **Link hỏng (404)**, kiểm trực tiếp bằng curl, không redirect. | — | — | Không đọc được nội dung |
| Hands-On Large Language Models (lnkd.in/dxaVF86w → HandsOnLLM/Hands-On-Large-Language-Models) | Sách O'Reilly (Alammar & Grootendorst), notebook theo chương: embeddings, fine-tuning, RAG, prompt engineering cơ bản (ch.6) | Nền, chương 6 có vài kỹ thuật prompt cơ bản nhưng không bàn system prompt cho agent | — | — |
| GenAI Agents, link 2 (lnkd.in/dEt72MEy → NirDiamant/GenAI_Agents) | Trùng với kho đã có trong office (xem docs/research/agent-repos.md) | Trùng | docs/research/agent-repos.md | — |
| Made with ML (lnkd.in/d2dMACMf) | **Link hỏng (404)** | — | — | Không đọc được nội dung |
| Awesome Generative AI Guide (lnkd.in/dJ8qxp3q) | **Link hỏng (404)** | — | — | Không đọc được nội dung |
| Designing Machine Learning Systems (lnkd.in/dEx8sQJK → chiphuyen/dmls-book) | Tóm tắt + tài nguyên cho sách Chip Huyen, có `mlops-tools.md` (danh sách công cụ chọn theo nhu cầu monitoring/serving) và `summary.md` | Nền là chính; phần MLOps (giám sát, lựa chọn công cụ) liên quan khái niệm tới quy trình vận hành agent | Office chưa có pipeline giám sát/đánh giá prompt riêng (chỉ có internal/prompts/prompts.go:37-68 là loader/template, không versioning) | Chưa có cơ chế theo dõi chất lượng output theo thời gian |
| ML for Beginners (Microsoft, lnkd.in/dBj3BAEY → microsoft/ML-For-Beginners) | Giáo trình ML cổ điển (scikit-learn), 12 tuần 26 bài | Nền | — | — |
| LLM Course (github.com/mlabonne/llm-course) | Khoá học LLM mã nguồn mở, 2 nhánh: Scientist và Engineer; nhánh Engineer có phần Agents (MCP, A2A, Claude Agent SDK, LangGraph, CrewAI) và Evaluation (Ragas, DeepEval, lm-evaluation-harness) | Tích hợp được phần Agents + Evaluation; phần Monitoring chỉ lướt qua (Langfuse); không có phần versioning prompt | — | Thiếu chi tiết về prompt versioning |

## Office hiện có liên quan

- `internal/prompts/prompts.go:37` (`Text`), `:47` (`Render` — template hoá theo data), `:56` (`parsed`), `:68` (`Names`) — loader prompt theo tên + template Go, không có version/lịch sử thay đổi, không gắn eval.
- Không tìm thấy module đánh giá (golden set, trace-based eval) nào trong `internal/` hiện tại.

## Đề xuất (tối đa 2)

1. **Trace-based eval cho prompt agent**: theo mẫu "Trace-Based Agent Evaluation" trong NirDiamant/GenAI_Agents — ghi lại trace (input/tool calls/output) của các lượt chat quan trọng thành bộ test cố định, chạy lại khi sửa `internal/prompts/*` để bắt regression trước khi merge.
2. **Theo dõi phiên bản prompt**: thêm version/changelog ngắn vào mỗi file trong `internal/prompts/` (hoặc một bảng versioning riêng) để biết lượt nào sửa gì, dễ rollback khi agent đổi hành vi bất ngờ sau khi sửa prompt.

→ Đã thêm 2 quest "[Tích hợp] …" qua `burn_quest` (xem bên dưới); cả hai đáng làm vì office hiện hoàn toàn thiếu cơ chế đánh giá/theo dõi phiên bản cho prompt, trong khi `internal/prompts/` đang lớn dần.
