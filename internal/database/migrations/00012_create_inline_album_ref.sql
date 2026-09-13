-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inline_album_ref (
    user_id BIGINT NOT NULL,
    extractor_id TEXT NOT NULL,
    content_id TEXT NOT NULL,
    message_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, extractor_id, content_id)
);

CREATE TABLE IF NOT EXISTS inline_album_payload (
    hash TEXT PRIMARY KEY,
    extractor_id TEXT NOT NULL,
    content_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inline_album_payload;
DROP TABLE IF EXISTS inline_album_ref;
-- +goose StatementEnd
