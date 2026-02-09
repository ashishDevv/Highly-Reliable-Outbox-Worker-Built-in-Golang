-- name: CreateIdentity :one
INSERT INTO 
auth_identities (user_id, email, password_hash)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetIdentityByEmail :one
SELECT id, user_id, email, password_hash, email_verified, revoked_at
FROM auth_identities
WHERE email = $1;

-- name: UpdateEmailVerified :exec
UPDATE auth_identities
SET email_verified = true
WHERE id = $1;

-- name: GetIdentityByUserID :one
SELECT id, user_id, email, password_hash, email_verified, revoked_at
FROM auth_identities
WHERE user_id = $1;

