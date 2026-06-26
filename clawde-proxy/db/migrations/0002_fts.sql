-- 0002_fts.sql — FTS5 full-text search index for chat_messages.
-- Purpose: Adds chat_messages_fts virtual table and sync triggers.
-- Ref: clawde-keystone-spec.md §7.
-- Applied by: db/migrations.go embedded FS runner (idempotent).

-- FTS5 virtual table over chat_messages content.
CREATE VIRTUAL TABLE IF NOT EXISTS chat_messages_fts USING fts5(
    id UNINDEXED,
    session_id UNINDEXED,
    content,
    content='chat_messages',
    content_rowid='rowid'
);

-- Keep FTS index in sync: insert.
CREATE TRIGGER IF NOT EXISTS chat_messages_fts_insert
AFTER INSERT ON chat_messages BEGIN
    INSERT INTO chat_messages_fts(rowid, id, session_id, content)
    VALUES (new.rowid, new.id, new.session_id, new.content);
END;

-- Keep FTS index in sync: delete.
CREATE TRIGGER IF NOT EXISTS chat_messages_fts_delete
AFTER DELETE ON chat_messages BEGIN
    INSERT INTO chat_messages_fts(chat_messages_fts, rowid, id, session_id, content)
    VALUES ('delete', old.rowid, old.id, old.session_id, old.content);
END;

-- Keep FTS index in sync: update.
CREATE TRIGGER IF NOT EXISTS chat_messages_fts_update
AFTER UPDATE ON chat_messages BEGIN
    INSERT INTO chat_messages_fts(chat_messages_fts, rowid, id, session_id, content)
    VALUES ('delete', old.rowid, old.id, old.session_id, old.content);
    INSERT INTO chat_messages_fts(rowid, id, session_id, content)
    VALUES (new.rowid, new.id, new.session_id, new.content);
END;
