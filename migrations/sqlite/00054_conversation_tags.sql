-- +goose Up
-- Tags a person puts on a chat to find it again; "Bug" and "bug" are one tag.
CREATE TABLE conversation_tags (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    tag             TEXT NOT NULL COLLATE NOCASE,
    created_at      TEXT NOT NULL,
    PRIMARY KEY (conversation_id, tag)
);
CREATE INDEX conversation_tags_tag ON conversation_tags (tag);

-- +goose Down
DROP TABLE conversation_tags;
