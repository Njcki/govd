-- name: GetInlineAlbumRef :one
SELECT user_id, extractor_id, content_id, message_id, created_at
FROM inline_album_ref
WHERE user_id = @user_id
  AND extractor_id = @extractor_id
  AND content_id = @content_id;

-- name: UpsertInlineAlbumRef :exec
INSERT INTO inline_album_ref (user_id, extractor_id, content_id, message_id)
VALUES (@user_id, @extractor_id, @content_id, @message_id)
ON CONFLICT (user_id, extractor_id, content_id) DO UPDATE
SET message_id = EXCLUDED.message_id,
    created_at = CURRENT_TIMESTAMP;

-- name: UpsertInlineAlbumPayload :exec
INSERT INTO inline_album_payload (hash, extractor_id, content_id)
VALUES (@hash, @extractor_id, @content_id)
ON CONFLICT (hash) DO UPDATE
SET extractor_id = EXCLUDED.extractor_id,
    content_id = EXCLUDED.content_id;

-- name: GetInlineAlbumPayload :one
SELECT hash, extractor_id, content_id, created_at
FROM inline_album_payload
WHERE hash = @hash;
