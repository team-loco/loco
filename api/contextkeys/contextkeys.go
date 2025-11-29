package contextkeys

type ContextKey string

const (
	UserKey             ContextKey = "user"
	UserIDKey           ContextKey = "userId"
	ExternalUsernameKey ContextKey = "externalUsername"
	RequestIDKey        ContextKey = "requestId"
	MethodKey           ContextKey = "method"
	PathKey             ContextKey = "path"
	SourceIPKey         ContextKey = "sourceIp"
)
