-- +goose Up
-- +goose StatementBegin
ALTER TABLE settings ADD COLUMN max_video_height INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings DROP COLUMN IF EXISTS max_video_height;
-- +goose StatementEnd
