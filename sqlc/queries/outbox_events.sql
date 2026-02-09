-- name: InsertOutboxEvent :exec
INSERT INTO 
outbox_events (event_type, payload)
VALUES ($1, $2);

-- name: GetOutboxEvents :many
SELECT id, event_type, payload 
FROM outbox_events
WHERE (
    status = 'PENDING'
    OR (status = 'PROCESSING' AND locked_at < now() - interval '30 seconds')
)
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: LockOutboxEvents :execrows
UPDATE outbox_events
SET 
    status = 'PROCESSING',
    locked_by = $1,
    locked_at = now()
WHERE id = ANY($2::UUID[]);

-- name: MarkOutboxEventProcessed :execrows
UPDATE outbox_events
SET
    status = 'PROCESSED',
    processed_at = now()
WHERE id = ANY($1::UUID[]);

