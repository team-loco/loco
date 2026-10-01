export DATABASE_URL=postgres://loco_user:loco_password@localhost:5432/loco
export MIGRATION_FILES=../migrations/001_initial_schema.sql,../migrations/002_apps_and_deployments.sql,../migrations/003_user_scopes.sql
compose="docker compose -f $(git rev-parse --show-toplevel)/compose.yaml"
$compose exec -T postgres dropdb -U loco_user --if-exists -f loco
$compose exec -T postgres createdb -U loco_user -O loco_user loco
go run .
