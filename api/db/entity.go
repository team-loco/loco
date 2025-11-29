package db

type Entity struct {
	Type string // e.g. "user", "project", "workspace", "organization"
	ID   int64  // unique identifier for the entity
}

type EntityScope struct {
	Entity Entity // associated entity
	Scope  string // e.g. "read", "write", "admin"
}
