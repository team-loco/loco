export DATABASE_URL=postgres://loco_user:@localhost:5432/loco
export MIGRATION_FILES=../migrations/001_initial_schema.sql,../migrations/002_apps_and_deployments.sql,../migrations/003_user_scopes.sql
dropdb loco --if-exists -f 
createdb loco -O loco_user
go run .