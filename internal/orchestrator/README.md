# internal/orchestrator

Điều phối một `run`: dựng prompt từ role template + memory + context, gọi runtime, parse output JSON, chuyển tiếp sang heartbeat/debate/worker dispatch. Đây là nơi duy nhất quyết định escalate/notify (agent chỉ emit JSON).

- Phụ thuộc: runtime, memory, blackboard, worker, notify, budget
- Milestone: M1 (manager đơn), M2 (director + debate)
