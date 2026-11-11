-- name: ConsumeAdmission :one
INSERT INTO api_admission (key, used, expires_at)
VALUES (sqlc.arg(key), 1, statement_timestamp() + sqlc.arg(window_seconds)::double precision * interval '1 second')
ON CONFLICT (key) DO UPDATE SET
    used = CASE WHEN api_admission.expires_at <= statement_timestamp() THEN 1 ELSE api_admission.used + 1 END,
    expires_at = CASE WHEN api_admission.expires_at <= statement_timestamp() THEN EXCLUDED.expires_at ELSE api_admission.expires_at END
RETURNING used, GREATEST(1, ceil(extract(epoch FROM (expires_at - statement_timestamp()))))::bigint AS retry_seconds;
