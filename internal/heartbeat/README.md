# internal/heartbeat

Vòng heartbeat của Manager: đọc memory → giao worker lấy delta → so baseline/giả thuyết → cập nhật memory, ghi finding → escalate nếu bất thường → đặt next_check_at theo mode (normal/suspicious/incident).

- Phụ thuộc: orchestrator, memory, blackboard
- Milestone: M1
