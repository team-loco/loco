package contextkeys

import "github.com/google/uuid"

type ContextKey string

const (
	EntityKey           ContextKey = "entity"
	EntityScopesKey     ContextKey = "entityScopes"
	ExternalUsernameKey ContextKey = "externalUsername"
	RequestIDKey        ContextKey = "requestId"
	MethodKey           ContextKey = "method"
	PathKey             ContextKey = "path"
	SourceIPKey         ContextKey = "sourceIp"
	TokenKey            ContextKey = "token"
	StackRestrictionKey ContextKey = "stackRestriction"
)

type StackRestriction struct {
	EnvironmentID uuid.UUID
	Name          string
}
