-- name: BindRoutePublisher :one
INSERT INTO route_publishers(app_domain, reserved_prefixes) VALUES ($1, COALESCE(sqlc.arg(reserved_prefixes)::text[], '{}')) RETURNING *;

-- name: GetRoutePublisher :one
SELECT * FROM route_publishers WHERE id = $1;

-- name: LockRoutePublisher :one
SELECT * FROM route_publishers WHERE id = $1 FOR UPDATE;

-- name: RenewRoutePublisherSession :one
UPDATE route_publishers SET session_id = sqlc.arg(session_id),
 session_expires_at = NOW() + sqlc.arg(session_seconds)::int * INTERVAL '1 second'
WHERE id = sqlc.arg(id)
 AND (session_id = sqlc.arg(session_id) OR session_expires_at IS NULL OR session_expires_at <= NOW())
RETURNING *;

-- name: ListRouteIntents :many
SELECT * FROM public_route_publications
WHERE publisher_id = $1 AND change_sequence > $2
ORDER BY change_sequence LIMIT $3;

-- name: ReportRoutePublication :one
UPDATE public_route_publications p SET
 acknowledged_revision = CASE WHEN sqlc.arg(published)::boolean THEN revision ELSE 0 END,
 acknowledged_at = CASE WHEN sqlc.arg(published)::boolean AND acknowledged_revision <> revision THEN NOW() ELSE acknowledged_at END,
 drain_until = CASE WHEN sqlc.arg(published)::boolean AND acknowledged_revision <> revision THEN NOW() + sqlc.arg(drain_seconds)::int * INTERVAL '1 second' ELSE drain_until END,
 message = sqlc.arg(message)
WHERE p.id = sqlc.arg(id) AND p.publisher_id = sqlc.arg(publisher_id)
 AND p.revision = sqlc.arg(revision)
 AND EXISTS (SELECT 1 FROM route_publishers b WHERE b.id = p.publisher_id
   AND b.session_id = sqlc.arg(session_id) AND b.session_expires_at > NOW())
RETURNING p.*;

-- name: EnqueueBoundRouteAssignments :exec
INSERT INTO public_routing_dirty(resource_id, environment_id)
SELECT resource_id, environment_id FROM resource_domains WHERE app_domain = $1
ON CONFLICT DO NOTHING;

-- name: ListPublicRoutingWork :many
SELECT resource_id, environment_id FROM public_routing_dirty WHERE next_attempt_at <= NOW()
ORDER BY next_attempt_at, resource_id, environment_id LIMIT $1;

-- name: ReschedulePublicRoutingWork :exec
UPDATE public_routing_dirty SET next_attempt_at = NOW() + sqlc.arg(retry_seconds)::int * INTERVAL '1 second'
WHERE resource_id = $1 AND environment_id = $2;

-- name: DeletePublicRoutingDirty :exec
DELETE FROM public_routing_dirty WHERE resource_id = $1 AND environment_id = $2;

-- name: ListResourceRoutePublications :many
SELECT * FROM public_route_publications WHERE resource_id = $1 AND environment_id = $2 ORDER BY hostname;

-- name: ListBoundResourceDomains :many
SELECT rd.domain, b.id AS publisher_id
FROM resource_domains rd JOIN route_publishers b ON b.app_domain = rd.app_domain
WHERE rd.resource_id = $1 AND rd.environment_id = $2 ORDER BY rd.domain;

-- name: NextRouteChangeSequence :one
UPDATE route_publishers SET change_sequence = change_sequence + 1 WHERE id = $1 RETURNING change_sequence;

-- name: UpsertRouteIntent :one
INSERT INTO public_route_publications(publisher_id, hostname, resource_id, environment_id, cluster_ids, withdrawing, change_sequence)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (hostname) DO UPDATE SET resource_id = EXCLUDED.resource_id, environment_id = EXCLUDED.environment_id, cluster_ids = EXCLUDED.cluster_ids,
 withdrawing = EXCLUDED.withdrawing, revision = public_route_publications.revision + 1,
 change_sequence = EXCLUDED.change_sequence, message = '', updated_at = NOW()
WHERE public_route_publications.resource_id = EXCLUDED.resource_id
 AND public_route_publications.environment_id = EXCLUDED.environment_id
 OR (public_route_publications.withdrawing AND public_route_publications.acknowledged_revision = public_route_publications.revision
 AND public_route_publications.drain_until <= NOW())
RETURNING *;

-- name: ListRoutingPlacements :many
SELECT p.*, EXISTS(SELECT 1 FROM public_routing_retirements r WHERE r.resource_id = p.resource_id
 AND r.environment_id = p.environment_id AND r.cluster_id = p.cluster_id) AS retiring
FROM placements p WHERE p.resource_id = $1 AND p.environment_id = $2 AND NOT p.desired_deleted
 AND NOT resource_suspended(p.resource_id)
 AND EXISTS (SELECT 1 FROM clusters c WHERE c.id = p.cluster_id AND c.removed_at IS NULL) ORDER BY p.id;

-- name: StageRoutingRetirement :exec
INSERT INTO public_routing_retirements(resource_id, environment_id, cluster_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: HasManagedPublicRoutes :one
SELECT EXISTS (
 SELECT 1 FROM resource_domains rd JOIN route_publishers b ON b.app_domain = rd.app_domain
 WHERE rd.resource_id = $1 AND rd.environment_id = $2
 UNION ALL
 SELECT 1 FROM public_route_publications p WHERE p.resource_id = $1 AND p.environment_id = $2
 AND (NOT p.withdrawing OR p.acknowledged_revision <> p.revision OR p.drain_until > NOW())
)::boolean AS managed;

-- name: ListReadyRoutingRetirements :many
SELECT r.* FROM public_routing_retirements r
WHERE r.resource_id = $1 AND r.environment_id = $2
 AND NOT EXISTS (SELECT 1 FROM public_route_publications p
 WHERE p.resource_id = r.resource_id AND p.environment_id = r.environment_id
 AND (r.cluster_id = ANY(p.cluster_ids) OR p.acknowledged_revision <> p.revision
 OR p.drain_until IS NULL OR p.drain_until > NOW()));

-- name: DeleteRoutingRetirement :exec
DELETE FROM public_routing_retirements WHERE resource_id = $1 AND environment_id = $2 AND cluster_id = $3;

-- name: EffectiveRoutingHostnames :many
SELECT rd.domain, rd.is_primary FROM resource_domains rd WHERE rd.resource_id = $1 AND rd.environment_id = $2
UNION ALL
SELECT hostname, false FROM public_route_publications p WHERE p.resource_id = $1 AND p.environment_id = $2
 AND NOT EXISTS(SELECT 1 FROM resource_domains rd WHERE rd.domain = p.hostname)
 AND (NOT p.withdrawing OR p.acknowledged_revision <> p.revision OR p.drain_until IS NULL
 OR p.drain_until > NOW())
ORDER BY is_primary DESC, domain;

-- name: LockRoutePublishers :many
SELECT id FROM route_publishers WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE;

-- name: LockPublicRoutingWork :exec
SELECT resource_id FROM public_routing_dirty WHERE resource_id = $1 AND environment_id = $2 FOR UPDATE;

-- name: LockRoutingAssignments :exec
LOCK TABLE resource_domains IN SHARE ROW EXCLUSIVE MODE;

-- name: CheckPublisherReservations :one
SELECT EXISTS(SELECT 1 FROM resource_domains rd, unnest(sqlc.arg(prefixes)::text[]) prefix
 WHERE rd.domain = split_part(rd.domain, '.', 1) || '.' || sqlc.arg(app_domain)::text
 AND starts_with(split_part(rd.domain, '.', 1), prefix))::boolean AS conflicts;

-- name: ListUnbootstrappedPublishers :many
SELECT id, app_domain FROM route_publishers WHERE NOT bootstrapped ORDER BY id;

-- name: MarkPublisherBootstrapped :exec
UPDATE route_publishers SET bootstrapped = true WHERE id = $1;
