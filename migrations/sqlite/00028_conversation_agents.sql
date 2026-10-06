-- +goose Up
-- Members of a chat (ADR-044): each agent its own session, what it has seen
-- and its context; leads of the built-in teams get operate by default.
CREATE TABLE conversation_agents (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL,
    agent_name      TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    runtime         TEXT NOT NULL DEFAULT '',
    last_message_id TEXT NOT NULL DEFAULT '',
    context_tokens  INTEGER NOT NULL DEFAULT 0,
    context_window  INTEGER NOT NULL DEFAULT 0,
    joined_at       TEXT NOT NULL,
    PRIMARY KEY (conversation_id, agent_id)
);
INSERT INTO conversation_agents (conversation_id, agent_id, agent_name, session_id, runtime, last_message_id, context_tokens, context_window, joined_at)
SELECT c.id, c.agent_id, c.agent_name, c.session_id, c.runtime,
       COALESCE((SELECT m.id FROM messages m WHERE m.conversation_id = c.id ORDER BY m.created_at DESC, m.id DESC LIMIT 1), ''),
       c.context_tokens, c.context_window, c.created_at
FROM conversations c WHERE c.agent_id IS NOT NULL;
UPDATE agents SET permissions = json_set(permissions, '$.level', 'operate', '$.read_only', json('false'))
WHERE tier = 'lead' AND key IN ('team-lead', 'assistant', 'executor')
  AND json_extract(permissions, '$.level') IS NULL AND json_type(permissions, '$.caps') IS NULL;
UPDATE agents SET instructions = 'Bạn là trưởng nhóm. Trong Việc của đội: làm rõ mục tiêu, giao cho đúng manager, tổng hợp và chốt, kết luận có dẫn chứng. Trong Chat: việc tự làm được thì tự làm; chỉ tag @agent khác khi thật sự cần.'
WHERE key = 'team-lead' AND instructions = 'Bạn là trưởng nhóm. Không tự làm việc chi tiết. Làm rõ mục tiêu, giao cho đúng manager, tổng hợp và chốt. Kết luận phải có dẫn chứng từ manager hoặc worker.';

-- +goose Down
DROP TABLE conversation_agents;
