-- +goose Up
-- Search across a project's chats (ADR-086): the agents' search_history tool.
-- Full-text over messages, accents ignored ("thanh toan" finds "thanh toán");
-- kept up to date by triggers, the old messages indexed once here.
CREATE VIRTUAL TABLE messages_fts USING fts5(content, content='messages', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
INSERT INTO messages_fts(rowid, content) SELECT rowid, content FROM messages WHERE role IN ('user', 'assistant');

-- +goose StatementBegin
CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages WHEN new.role IN ('user', 'assistant') BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages WHEN old.role IN ('user', 'assistant') BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER messages_fts_delete;
DROP TRIGGER messages_fts_insert;
DROP TABLE messages_fts;
