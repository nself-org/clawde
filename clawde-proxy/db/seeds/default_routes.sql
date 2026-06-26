-- default_routes.sql — seed the local/ollama fallback lane in proxy_routes.
-- Purpose: Ensures a working fallback lane exists on first startup.
-- Ref: clawde-keystone-spec.md §7.
-- Applied by: migrations.go after 0003_vec.sql (idempotent via INSERT OR IGNORE).

INSERT OR IGNORE INTO proxy_routes (id, lane, upstream, priority, enabled)
VALUES (
    'route-local-default',
    'local',
    'http://localhost:11434',
    0,
    1
);
