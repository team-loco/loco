-- name: GetDeploymentRequest :one
SELECT * FROM deployment_requests WHERE id = $1;

-- name: ClaimDeploymentRequest :one
INSERT INTO deployment_requests (id, resource_id, fingerprint)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET id = deployment_requests.id
RETURNING *;

-- name: CompleteDeploymentRequest :exec
UPDATE deployment_requests SET response = $2 WHERE id = $1;
