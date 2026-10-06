-- +goose Up
-- When each person last looked at each chat: a reply after it is unread.
-- What is there now counts as seen (no flood of old chats on the first day).
CREATE TABLE conversation_reads (
    user_id         TEXT NOT NULL,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seen_at         TEXT NOT NULL,
    PRIMARY KEY (user_id, conversation_id)
);
INSERT INTO conversation_reads (user_id, conversation_id, seen_at)
SELECT u.id, c.id, strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z' FROM users u CROSS JOIN conversations c;

-- +goose Down
DROP TABLE conversation_reads;
