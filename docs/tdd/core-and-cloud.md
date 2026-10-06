# Technical Design Document: Core and Cloud

**Status:** Proposed.

## Summary

Loco ships three ways:

- **Multi-tenant SaaS.** Loco runs the control plane and shared worker clusters, and anyone can sign up.
- **Dedicated.** Loco runs it for a single customer, with clusters (and optionally a control plane) that no one else uses.
- **On-prem.** The customer runs all of it on their own infrastructure.

All three run the same product. What differs is who can sign up, who pays and who operates it. This document splits the code to match:

- **Core** is this repository, MIT licensed. It is everything an organization needs to run Loco for its own engineers: users, organizations, roles, login, invitations, quotas, usage measurement, notifications and administration.
- **Cloud** is a private repository, `loco-cloud`. It holds only what a business selling Loco needs: public signup gating, billing, plans, abuse handling, enterprise SSO brokering and an operator console.

Cloud imports core as a library and plugs into a small set of extension points. Core never imports cloud, and it carries no feature flags for cloud behaviour.

## The test for where a feature goes

Ask whether an on-prem customer running Loco for their own engineers would need the feature. If they would, it is core. If only a company selling Loco to strangers needs it, it is cloud.

| Feature | Home | Notes |
|---|---|---|
| Users, organizations, workspaces, roles, API tokens | core | Exists today (`users`, `user_scopes`, TVM). |
| Login with GitHub | core | Exists today. |
| Login with any OIDC provider | core | Covers Google, Okta, Entra ID, Keycloak and Dex. |
| SAML | cloud, or a broker in front of core | On-prem installs put Dex or Keycloak in front of core's OIDC. Cloud uses a SAML broker and sells SSO on its enterprise plan. |
| Signup modes: open, invite-only, allowed email domains | core | On-prem needs "only `@company.com`" or "only through our IdP". |
| Waitlist, beta codes, abuse scoring at signup | cloud | Implemented through the signup policy hook. |
| Organization invitations | core | Every multi-person install needs them. |
| Email delivery | core interface, SMTP default | Cloud supplies a transactional provider and lifecycle mail. |
| Quotas (apps, CPU, memory, replicas per organization) | core | Quotas are data, so an on-prem admin sets them by hand and cloud sets them from a plan. |
| Usage measurement | core | On-prem wants chargeback and the Usage page. |
| Pricing, plans, invoices, payment | cloud | |
| Suspending an organization | core | Admins suspend on-prem too. Cloud calls it on non-payment or abuse. |
| System administration (all orgs, clusters, users) | core | Uses the existing `system` entity type in `user_scopes`. |
| Operator console for the hosted service (support tooling, abuse review, revenue) | cloud | |
| Marketing site, pricing page | cloud | |

## Extension points in core

Core gains four seams. Each has a working default inside core, so an on-prem install never needs cloud.

### 1. The server is a library

Today `api/main.go` reads config and builds every service inline in `package main`, which nothing else can import. Move the wiring into an importable package:

```go
package server

type Options struct {
	Config        Config
	SignupPolicy  SignupPolicy
	Notifier      Notifier
	Identity      []IdentityProvider
	ExtraHandlers []Handler
}

func New(ctx context.Context, opts Options) (*Server, error)
func (s *Server) Run(ctx context.Context) error
```

`api/main.go` becomes a short function that builds `Options` from the environment with core defaults and calls `server.New`. `loco-cloud/cmd/api/main.go` does the same with cloud implementations and mounts its own ConnectRPC handlers through `ExtraHandlers`. Migrations stay in core. Cloud keeps its own tables in a separate Postgres schema, `cloud`, with its own goose history, so core migrations never depend on cloud.

### 2. Identity providers and signup policy

`tvm.Exchange` already takes a provider identity and finds or rejects a user, and `users.external_id` already stores `provider:id`. Two changes generalise this:

- An `IdentityProvider` interface with GitHub and generic OIDC implementations. OIDC users are stored as `oidc:<issuer>:<subject>`. The issuer and client come from config, and an install can enable several providers.
- A `SignupPolicy` consulted when an identity has no user yet. This replaces `tempCreateUser` and its "remove once invitations exist" todo in `api/service/oauth.go`.

```go
type SignupRequest struct {
	Identity   Identity
	Invitation *Invitation
}

type SignupDecision struct {
	Allow  bool
	Reason string
}

type SignupPolicy interface {
	Decide(ctx context.Context, req SignupRequest) (SignupDecision, error)
}
```

Core ships one implementation driven by config: `open`, `invite-only`, or `domains` with a list of allowed email domains. A valid invitation always allows signup. Cloud's implementation checks the waitlist, beta codes and abuse signals, then falls back to core's.

### 3. Event outbox

Services write domain events to an `events` table in the same transaction as the change they describe: `user.created`, `org.created`, `invitation.created`, `deployment.succeeded`, `deployment.failed`, `resource.scaled`, `org.suspended` and `usage.rolled_up`. Each event has a type, the org it belongs to, a JSON payload and a sequence number.

Consumers read in sequence order and record their own cursor. They are woken with LISTEN/NOTIFY the same way `clusternotify` wakes the Sync stream, and fall back to polling. #277 applies here as well: LISTEN needs a session connection if a pooler is added.

Core's own consumer sends notification email. Cloud's consumers push usage to the billing provider, send lifecycle mail and feed analytics. A billing outage then delays an invoice; it never fails a deploy.

The table also serves as the audit log that `checklist.txt` asks for, with a retention period set per install.

### 4. Capabilities for clients

`ConfigService` already returns the default platform domain and the minimum CLI version. It also returns a list of capabilities, such as `billing`, `invitations-email` and `identity:oidc`, plus links like the billing portal URL. The web app shows a Billing entry only when the API reports it, and that entry links to the billing provider's hosted portal. Cloud therefore needs no fork of the web app at first. If cloud later needs pages of its own, they go in a separate app on its own subdomain.

## New core features these seams need

**Invitations.** An `org_invitations` table holds the org, invited email, granted scopes, a token hash, who sent it, expiry and acceptance time. When a Notifier is configured, core emails the link. When none is configured (a fresh on-prem install with no SMTP), the dashboard shows the link to copy. An invitation can only grant scopes its sender holds, which is the same no-escalation rule as API tokens. Accepting it passes signup policy through `SignupRequest.Invitation`.

**Notifier.** `Notify(ctx, Message)`, where a message is a template ID, a recipient and data. Core ships SMTP and a no-op default. Templates for invitations and deploy failures live in core.

**Quotas.** An `org_quotas` row holds maximum apps, CPU, memory and total replicas. The API checks it on deploy and scale, and the controller turns it into a `ResourceQuota` on the workspace namespace, so it holds even if the API check is bypassed. On-prem defaults to no limits. Cloud writes the row when a plan changes.

**Usage.** A periodic job rolls up CPU and memory reservation per app per hour into a `usage` table and emits `usage.rolled_up`. Usage is measured from reservations, not observed load, so a bill never depends on telemetry sampling.

**Suspension.** `organizations.suspended_at` blocks deploys and scale-ups and scales the org's apps to zero. The system admin API exposes it, and so does cloud's consumer of failed-payment events.

## Deployment modes

| | SaaS | Dedicated | On-prem |
|---|---|---|---|
| Control plane | Loco's, shared | Loco's shared one, or a single-tenant install | Customer's |
| Worker clusters | Shared | Customer-only | Customer's |
| Binary | `loco-cloud` | `loco-cloud` | core |
| Signup | Cloud policy | `invite-only` or `domains` | Customer's choice |
| Billing | Usage and plans | Contract, so usage is reported but not charged per unit | None |

Dedicated starts as dedicated worker clusters attached to the shared control plane. The agent already dials out and placement already chooses among clusters, so this needs only a way to pin an organization's placements to its own clusters. A fully single-tenant control plane is the on-prem install, operated by Loco.

On-prem needs things that don't exist yet:

- A Helm chart for the API and web app. They are deployed to Railway today through `.railway/railway.go`.
- A pluggable image registry. User images go to a hard-coded GitLab registry today. The registry proposal in the pluggable-dependencies design covers this.
- Documentation for bringing your own Postgres, ClickHouse and DNS.

## Rules

- Core has no `if cloud` branches, build tags or flags that mean "hosted". Behaviour differences enter only through the four seams and through config.
- Cloud never writes to core tables directly. It goes through core's Go API or the system admin RPCs, so core's invariants hold.
- Anything an on-prem admin would need in order to operate an install is core, even when cloud is the first consumer.

## Order of work

1. Server as a library and `SignupPolicy` with core's config-driven modes. This replaces `tempCreateUser` and unblocks the beta.
2. Organization invitations, plus the Notifier with SMTP.
3. Generic OIDC provider.
4. Event outbox, and suspension.
5. Quotas, enforced in the API and as `ResourceQuota`.
6. Usage rollups. After this, cloud can start billing.
7. API and web Helm chart and a pluggable registry for on-prem.

The `loco-cloud` repository starts at step 1 with its signup policy and waitlist, and adds billing after step 6.

## Open questions

- **Account linking.** `users.email` is unique, so signing in with OIDC under an email already used with GitHub fails today. Should a verified email link the two identities, or should linking require an explicit action while signed in?
- **Where cloud's web pages live** once the hosted portal is not enough: a separate app, or a plugin slot in core's web app.
- **Usage pricing.** Reservation-based usage is simple and predictable, but it charges for idle reservations. Does Loco also need observed-usage metering for scale-to-zero?
