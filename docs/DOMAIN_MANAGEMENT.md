# Domain Management Architecture

## Overview

Loco supports two types of domain management:

1. **Platform-managed subdomains** - Loco controls the base domain (e.g., `loco.dev`)
2. **User-managed custom domains** - Users bring their own domains (e.g., `example.com`)

## Current Structure

### Database Schema

**`domain_source` enum (PostgreSQL type):**

```sql
CREATE TYPE domain_source AS ENUM ('platform_provided', 'user_provided');
```

**`platform_domains` table:**

- Base domains provided by Loco operators
- Example: `loco.dev`, `deploy-app.com`, `myapp.io`
- Marked `is_active` to enable/disable platform domains
- Indexed on `is_active` for quick lookup of available domains

**`app_domains` table:**

- Stores multiple domains per app (many-to-one relationship)
- `domain` - Fully-qualified hostname (e.g., `myapp.loco.dev`, `example.com`)
- `domain_source` enum: `'platform_provided'` or `'user_provided'`
- `subdomain_label` - Only populated for platform-provided domains (e.g., `myapp`)
- `platform_domain_id` - Only populated for platform-provided domains
- `is_primary` - Marks the canonical domain for the app (max 1 per app, enforced by index)
- Database constraints ensure data integrity:
  - PLATFORM_PROVIDED domains: `subdomain_label IS NOT NULL` AND `platform_domain_id IS NOT NULL`
  - USER_PROVIDED domains: `subdomain_label IS NULL` AND `platform_domain_id IS NULL`
  - Only one `is_primary = true` per app (via partial unique index)

## Problem with Current Naming

**Old enum names were confusing:**

- `SUBDOMAIN` - doesn't clarify who manages it (Loco or user)
- `CUSTOM` - doesn't clarify ownership or management

**User question:** "Can't user-brought domains also use subdomains?"

Answer: YES. A user can bring `example.com` and deploy on `foo.example.com`. Both are the same type: USER-PROVIDED.

Old naming obscured this: it conflated "syntax" (subdomain vs full domain) with "ownership" (platform vs user).

## New Naming Scheme

Renamed to clarify **WHO MANAGES** the domain:

### `domain_source` enum (PostgreSQL type)

```
PLATFORM_PROVIDED
  - Loco controls the base domain
  - Users provide the subdomain prefix
  - Example: `myapp.loco.dev`
  - Has subdomain_label and platform_domain_id
  - Schema requires both fields to be NOT NULL

USER_PROVIDED
  - User controls entire domain
  - Can be subdomain or full domain
  - Examples: `example.com`, `api.example.com`
  - subdomain_label and platform_domain_id are NULL
  - User owns all DNS configuration
```

### Why this is better

- **Clearest intent**: indicates source/ownership of the domain
- **Self-documenting**: `domain_source = PLATFORM_PROVIDED` is unambiguous
- **Aligns with business logic**: who is responsible for domain management?
- **PostgreSQL ENUM**: enforces valid values at database level
- **Separates concerns**: `subdomain_label` field enables querying/tracking subdomains separately from full domain

## Database Migration

### Final Schema

```sql
-- ===============================================
-- DOMAIN MANAGEMENT SCHEMA
-- ===============================================

-- Domain source enum (ownership)
CREATE TYPE domain_source AS ENUM (
  'platform_provided',
  'user_provided'
);

-- Platform-managed base domains
CREATE TABLE platform_domains (
  id BIGSERIAL PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_platform_domains_active ON platform_domains(is_active);

-- Per-app domain mapping (multiple domains per app)
CREATE TABLE app_domains (
  id BIGSERIAL PRIMARY KEY,

  -- Many-to-one: each app can own multiple domains
  app_id BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,

  -- Canonical fully-qualified hostname
  domain TEXT NOT NULL UNIQUE,

  -- Domain ownership/source
  domain_source domain_source NOT NULL,

  -- Platform-provided specific fields (NULL for user_provided)
  subdomain_label TEXT,
  platform_domain_id BIGINT REFERENCES platform_domains(id),

  -- Primary domain flag (max 1 per app, enforced by unique partial index)
  is_primary BOOLEAN NOT NULL DEFAULT false,

  -- Timestamps
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  -- Cross-field integrity
  CONSTRAINT domain_source_check CHECK (
    (domain_source = 'platform_provided' AND subdomain_label IS NOT NULL AND platform_domain_id IS NOT NULL)
    OR
    (domain_source = 'user_provided' AND subdomain_label IS NULL AND platform_domain_id IS NULL)
  )
);

CREATE INDEX idx_app_domains_domain ON app_domains(domain);
CREATE INDEX idx_app_domains_app_id ON app_domains(app_id);

-- Enforce max 1 primary domain per app
CREATE UNIQUE INDEX uniq_app_primary_domain
  ON app_domains(app_id)
  WHERE is_primary = true;

-- Ensure platform subdomain uniqueness
CREATE UNIQUE INDEX uniq_platform_subdomain
  ON app_domains(platform_domain_id, subdomain_label)
  WHERE domain_source = 'platform_provided';
```

## Implementation Plan

### Phase 1: Update Proto Definition

- [ ] Update `DomainType` enum in `shared/proto/domain/v1/domain.proto`:
  - Rename `SUBDOMAIN = 0;` → `PLATFORM_PROVIDED = 0;`
  - Rename `CUSTOM = 1;` → `USER_PROVIDED = 1;`
- [ ] Run `buf generate` to regenerate Go code

### Phase 2: Create Database Migration

- [ ] Create new migration file (e.g., `003_rename_domain_types.sql`)
- [ ] Add PostgreSQL ENUM type creation
- [ ] Add `subdomain_label` column to `app_domains`
- [ ] Populate `subdomain_label` from existing `domain` values (for platform domains)
- [ ] Drop old `domain_type` VARCHAR column
- [ ] Add new `domain_source` column with proper defaults
- [ ] Add new constraints

### Phase 3: Update Database Layer

- [ ] Update enum handling in `api/service/app.go`:
  - Convert `PLATFORM_PROVIDED` ↔ `"platform_provided"` string
  - Convert `USER_PROVIDED` ↔ `"user_provided"` string
  - Extract `subdomain_label` when creating platform domains
  - Centralize conversion logic in helper function

-- Stop Here --

### Phase 4: Update Frontend

- [ ] Update `web/src/pages/CreateApp.tsx`:
  - Import updated `DomainType` from generated proto
  - Use `DomainType.PLATFORM_PROVIDED` for platform domains
  - Update UI labels: "Platform-Managed Domain" instead of "Subdomain"
  - Update helper text clarity

## Key Business Logic

### Domain Availability Check

- **Always required**: Domain must be unique across all apps
- **No ownership validation yet**: Any app can claim any domain
- Future: Add DNS verification for custom domains

### Platform Domain Selection

- **Required for platform-managed**: App must select which platform base domain to use
- **Not applicable for custom**: User brings complete domain

### Domain Uniqueness Constraint

- `app_domains.domain` UNIQUE constraint enforces global uniqueness
- Same validation in code: `CheckDomainAvailability`

## Code Locations to Update

1. **Proto**: `shared/proto/domain/v1/domain.proto`

   - DomainType enum

2. **Backend**:

   - `api/service/app.go` - CreateApp domain type conversion
   - `api/service/domain.go` - Domain type handling
   - SQL migrations (documentation only, no schema change needed)

3. **Frontend**:

   - `web/src/pages/CreateApp.tsx` - Domain selection UI
   - Labels and help text throughout

4. **Documentation**:
   - This file (already started)
   - Proto file comments
   - Code inline comments

## Examples

### Platform-Provided Domain

```
User input: subdomain = "myapp"
Selected platform domain: "loco.dev"

Database result:
  domain = "myapp.loco.dev"
  domain_source = 'platform_provided'
  subdomain_label = "myapp"
  platform_domain_id = 1
```

### User-Provided Domain

```
User input: domain = "api.example.com"

Database result:
  domain = "api.example.com"
  domain_source = 'user_provided'
  subdomain_label = NULL
  platform_domain_id = NULL
```

## Field Meanings

**`domain`** - The complete, canonical hostname that traffic routes to

- Always populated
- UNIQUE across all apps
- Checked for availability before creating app

**`domain_source`** - Who owns/manages this domain

- `'platform_provided'` = Loco manages base, user provides subdomain prefix
- `'user_provided'` = User brings complete domain, all DNS is their responsibility

**`subdomain_label`** - Only for platform-provided domains

- The user-supplied prefix (e.g., "myapp" from "myapp.loco.dev")
- Enables querying: "all subdomains under loco.dev"
- NULL for user-provided domains

**`platform_domain_id`** - Reference to Loco's base domain

- Only for platform-provided domains
- Enables multi-tenancy on shared base domains
- NULL for user-provided domains

**`is_primary`** - Marks this as the canonical domain for the app

- True for exactly one domain per app (enforced by unique partial index)
- Used for redirects, certificate prioritization, and UI display
- Must have a plan for what happens when primary domain is deleted
- Default false for all new domains (application must set primary)

## Future Considerations

1. **DNS Verification**: Require TXT record for user-provided domains (Phase 3+)
2. **Domain Status Lifecycle**: Track pending/active/disabled/revoked states (Phase 4+)
3. **Domain Transfer**: Allow moving domain between apps
4. **Primary Domain Auto-Promotion**: When primary domain is deleted, auto-promote oldest remaining domain
5. **Wildcard Domains**: Support `*.example.com` for user-provided domains
6. **Automatic Subdomain Generation**: If user doesn't provide one, auto-generate
