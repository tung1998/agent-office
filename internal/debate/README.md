# internal/debate

State machine debate cho một incident: `independent` → `cross_review` (≤ N vòng) → `synthesis` bởi Director. Enforce token budget / max rounds, chặn manager đọc ý nhau ở vòng independent.

- Phụ thuộc: blackboard, orchestrator, budget
- Milestone: M2
