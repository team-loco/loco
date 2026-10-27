package contextkeys

type ContextKey string

const (
	EntityKey           ContextKey = "entity"
	EntityScopesKey     ContextKey = "entityScopes"
	ExternalUsernameKey ContextKey = "externalUsername"
	RequestIDKey        ContextKey = "requestId"
	PathKey             ContextKey = "path"
	TokenKey            ContextKey = "token"
	IdentityKey         ContextKey = "identity"
	ClaimedOrgsKey      ContextKey = "claimedOrgs"
)
