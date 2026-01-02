package contextkeys

type ContextKey string

const (
	EntityKey           ContextKey = "entityType"
	EntityScopesKey     ContextKey = "entityScopes"
	ExternalUsernameKey ContextKey = "externalUsername"
	RequestIDKey        ContextKey = "requestId"
	MethodKey           ContextKey = "method"
	PathKey             ContextKey = "path"
	SourceIPKey         ContextKey = "sourceIp"
)
