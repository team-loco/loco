// Package limits holds the count limits: the install defaults, an organization's overrides and
// the error a create past a cap returns. Checks and history prunes read limits only from an
// Effective, which ForOrg and Install build, so a caller cannot enforce limits it never loaded.
package limits

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/reflect/protoreflect"

	genDb "github.com/team-loco/loco/api/gen/db"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

// ErrLimitReached is the error of a create that a count limit refuses.
var ErrLimitReached = rpcerr.NewCoded(connect.CodeResourceExhausted, "count limit reached")

var errNotLoaded = errors.New("count limits not loaded")

// Effective is a set of limits loaded for enforcement. Its zero value holds none, and Get and Check
// refuse it.
type Effective struct {
	counts *configv1.CountLimits
}

// Install returns the install defaults as the effective limits, for limits no organization
// overrides, such as organizations per user.
func Install(defaults *configv1.CountLimits) Effective {
	return Effective{counts: defaults}
}

// Limit is one field of CountLimits.
type Limit struct {
	// Field is the CountLimits field and the org_limit_overrides column.
	Field protoreflect.Name
	// Env is the API environment variable that sets the install default.
	Env      string
	fallback int32
	// counted describes what a cap refuses, with the current count and the limit as verbs.
	counted  string
	override func(*genDb.OrgLimitOverride) **int32
}

var (
	OrgsPerUser = Limit{
		Field: "orgs_per_user", Env: "LOCO_LIMIT_ORGS_PER_USER", fallback: 10,
		counted: "you created %d of %d organizations",
	}
	WorkspacesPerOrg = Limit{
		Field: "workspaces_per_org", Env: "LOCO_LIMIT_WORKSPACES_PER_ORG", fallback: 100,
		counted:  "the organization has %d of %d workspaces",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.WorkspacesPerOrg },
	}
	MembersPerOrg = Limit{
		Field: "members_per_org", Env: "LOCO_LIMIT_MEMBERS_PER_ORG", fallback: 500,
		counted:  "the organization has %d of %d members",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.MembersPerOrg },
	}
	InvitationsPerOrg = Limit{
		Field: "invitations_per_org", Env: "LOCO_LIMIT_INVITATIONS_PER_ORG", fallback: 100,
		counted:  "the organization has %d of %d open invitations",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.InvitationsPerOrg },
	}
	EnvironmentsPerWorkspace = Limit{
		Field: "environments_per_workspace", Env: "LOCO_LIMIT_ENVIRONMENTS_PER_WORKSPACE", fallback: 10,
		counted:  "the workspace has %d of %d environments",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.EnvironmentsPerWorkspace },
	}
	ResourcesPerWorkspace = Limit{
		Field: "resources_per_workspace", Env: "LOCO_LIMIT_RESOURCES_PER_WORKSPACE", fallback: 100,
		counted:  "the workspace has %d of %d resources",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.ResourcesPerWorkspace },
	}
	WebhooksPerWorkspace = Limit{
		Field: "webhooks_per_workspace", Env: "LOCO_LIMIT_WEBHOOKS_PER_WORKSPACE", fallback: 20,
		counted:  "the workspace has %d of %d webhooks",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.WebhooksPerWorkspace },
	}
	TokensPerEntity = Limit{
		Field: "tokens_per_entity", Env: "LOCO_LIMIT_TOKENS_PER_ENTITY", fallback: 50,
		counted:  "the token's owner has %d of %d API tokens",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.TokensPerEntity },
	}
	DomainsPerResource = Limit{
		Field: "domains_per_resource", Env: "LOCO_LIMIT_DOMAINS_PER_RESOURCE", fallback: 10,
		counted:  "the resource has %d of %d domains in the environment",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.DomainsPerResource },
	}
	SecretsPerResource = Limit{
		Field: "secrets_per_resource", Env: "LOCO_SECRETS_MAX_PER_SERVICE", fallback: 256,
		counted:  "the service has %d of %d secrets in the environment",
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.SecretsPerResource },
	}
	DeploymentHistory = Limit{
		Field: "deployment_history", Env: "LOCO_LIMIT_DEPLOYMENT_HISTORY", fallback: 100,
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.DeploymentHistory },
	}
	BuildHistory = Limit{
		Field: "build_history", Env: "LOCO_LIMIT_BUILD_HISTORY", fallback: 100,
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.BuildHistory },
	}
	LogRetentionDays = Limit{
		Field: "log_retention_days", Env: "LOCO_LIMIT_LOG_RETENTION_DAYS", fallback: 365,
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.LogRetentionDays },
	}
	TraceRetentionDays = Limit{
		Field: "trace_retention_days", Env: "LOCO_LIMIT_TRACE_RETENTION_DAYS", fallback: 365,
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.TraceRetentionDays },
	}
	MetricRetentionDays = Limit{
		Field: "metric_retention_days", Env: "LOCO_LIMIT_METRIC_RETENTION_DAYS", fallback: 365,
		override: func(o *genDb.OrgLimitOverride) **int32 { return &o.MetricRetentionDays },
	}
)

// All lists every limit in CountLimits field order.
var All = []Limit{
	OrgsPerUser, WorkspacesPerOrg, MembersPerOrg, InvitationsPerOrg,
	EnvironmentsPerWorkspace, ResourcesPerWorkspace, WebhooksPerWorkspace, TokensPerEntity,
	DomainsPerResource, SecretsPerResource, DeploymentHistory, BuildHistory,
	LogRetentionDays, TraceRetentionDays, MetricRetentionDays,
}

// Defaults returns the built-in defaults; environment variables replace them at startup.
func Defaults() *configv1.CountLimits {
	counts := &configv1.CountLimits{}
	for _, limit := range All {
		limit.Set(counts, limit.fallback)
	}
	return counts
}

// Get returns the limit's value. Every loaded limit is positive, so Get refuses a value that is
// not: read as 0, a history limit would prune every finished record.
func (l Limit) Get(effective Effective) (int32, error) {
	value := l.Value(effective.counts)
	if value <= 0 {
		return 0, fmt.Errorf("%w: %s is %d", errNotLoaded, l.Field, value)
	}
	return value, nil
}

// Value returns the limit's field in a CountLimits message at the API boundary: a request, where 0
// clears an override, a response or the install defaults being read.
func (l Limit) Value(counts *configv1.CountLimits) int32 {
	return int32(counts.ProtoReflect().Get(l.descriptor()).Int())
}

// Set stores value as the limit's value in counts.
func (l Limit) Set(counts *configv1.CountLimits, value int32) {
	counts.ProtoReflect().Set(l.descriptor(), protoreflect.ValueOfInt32(value))
}

// Check refuses adding records to existing ones when the total would pass the limit.
func (l Limit) Check(effective Effective, existing, adding int64) error {
	limit, err := l.Get(effective)
	if err != nil {
		return err
	}
	if existing+adding <= int64(limit) {
		return nil
	}
	return ErrLimitReached.Detail(
		l.counted+"; delete one or ask a Loco administrator to raise %s",
		existing, limit, l.Env,
	)
}

// Overridable reports whether an organization can override the limit.
func (l Limit) Overridable() bool {
	return l.override != nil
}

func (l Limit) descriptor() protoreflect.FieldDescriptor {
	return (&configv1.CountLimits{}).ProtoReflect().Descriptor().Fields().ByName(l.Field)
}

// Column returns the address of the limit's column in an override row. It panics for a limit
// an organization cannot override.
func (l Limit) Column(o *genDb.OrgLimitOverride) **int32 {
	return l.override(o)
}

// ForOrg returns defaults with the organization's overrides applied.
func ForOrg(
	ctx context.Context,
	q genDb.Querier,
	defaults *configv1.CountLimits,
	orgID uuid.UUID,
) (Effective, error) {
	row, err := q.GetOrgLimitOverrides(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Effective{counts: defaults}, nil
	}
	if err != nil {
		return Effective{}, fmt.Errorf("get organization limits: %w", err)
	}
	return Effective{counts: Apply(defaults, row)}, nil
}

// Apply returns defaults with the columns set in an override row replacing them, for responses
// that report an organization's limits.
func Apply(defaults *configv1.CountLimits, row genDb.OrgLimitOverride) *configv1.CountLimits {
	effective := &configv1.CountLimits{}
	for _, limit := range All {
		value := limit.Value(defaults)
		if limit.Overridable() {
			if set := *limit.Column(&row); set != nil {
				value = *set
			}
		}
		limit.Set(effective, value)
	}
	return effective
}
