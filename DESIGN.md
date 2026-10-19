---
version: alpha
name: Loco
description: A compact operations dashboard for developers deploying services.
---

## Overview

Loco is a developer's operations console: find the affected service, understand its current state, then inspect or change it. Preserve its compact tables, warm neutral surfaces, quiet borders and restrained blue actions. Names, status and telemetry carry the hierarchy; decorative panels and large promotional headings do not.

The reference screens are the [workspace dashboard](web/src/pages/Dashboard.tsx), [resource overview](web/src/pages/Resource.tsx), [workspace settings](web/src/pages/settings/WorkspaceTab.tsx) and [Traces](web/src/pages/observability/TracesView.tsx). The English UI uses system fonts. Additional locales or market-specific formats need their own evidence; these screens do not establish them.

This guide is grounded in a production build at `5fe392bb`, reviewed against the local astronomy-shop demo on 2026-10-16. The review covers light/dark themes at desktop and narrow widths, including populated tables, an unavailable Events section and a local form-validation state. Screenshots accompany the guide's PR. Real read-only API responses are recorded and reused during capture; form editing is local and no changes are submitted.

## Colors

[`index.css`](web/src/index.css) owns the light/dark CSS variables and their Tailwind aliases. Use semantic classes through the design wrappers; this document explains their roles rather than maintaining another palette.

| Role | Tokens and treatment | Existing example |
| --- | --- | --- |
| Main content | `bg-background`, `text-foreground` | Resource name, operation name and editable form values |
| Secondary content | `text-fg2`, `text-fg3` | Deployment explanation, timestamps and settings hints |
| Low-emphasis decoration | `text-fg4`, `border-line`, `border-line2` | Chart guides, separators and placeholders; do not use this as the only treatment for essential status |
| Grouping and selection | `bg-bg2`, `bg-bg3` | Table headers, hovered rows, selected navigation and segmented controls |
| Primary action | `bg-primary`, `text-primary-foreground`, `primary-hover` | Create resource and Redeploy; keep neighboring navigation and secondary actions outlined or quiet |
| Status | `ok`, `info`, `warn`, `bad`, `neutral` background/foreground pairs | Healthy badge, managed configuration notice, warning and validation error |
| Destructive action | The Button's destructive variants and DangerCard | Delete workspace, separated from normal settings |

Use [StatusBadge](web/src/components/design/StatusBadge.tsx) for resource/deployment state and [Badge](web/src/components/design/Badge.tsx) for generic tones. Pair status color with a readable label or icon; a colored dot alone is insufficient. Theme changes preserve the meaning and contrast hierarchy, including overlays and disabled controls.

Service colors in traces identify services; error markers identify failures. Keep those roles separate. Trace colors come from [trace-service](web/src/pages/observability/trace-service.ts) and the theme's trace variables. Reuse the existing chart and trace styles rather than introducing a new chart palette.

## Typography

The font families, size scale and line heights live in [`index.css`](web/src/index.css). Use the existing role and wrapper instead of choosing a new size for each screen.

| Role | Recipe | Application |
| --- | --- | --- |
| Operational page title | [PageHeader](web/src/components/design/Page.tsx), `text-2xl font-semibold` | Overview, Settings and the resource name |
| Section and form label | Section's title treatment; settings' semibold row label | Resources, Deployments, Name and Description |
| Reading and interaction | `text-base`, inherited system sans | Table cells, buttons and inputs |
| Supporting information | `text-sm`/`text-xs`, `text-fg2`/`text-fg3` | Counts, time bounds, hint text and secondary metadata |
| Technical values | `font-mono`; `tabular-nums` for changing numbers | Trace IDs, durations, timestamps and replica counts |

Use weight and alignment before adding size. In the resource header, the service name leads, status sits beside it, and image/deployment metadata follows underneath. In Traces, the operation leads each row while service and ID recede. Keep technical rows single-line where comparison depends on alignment, with full content available in details; wrap explanatory text and validation messages.

Observability uses a compact query-first shell rather than the large operational page header. [Observability](web/src/pages/Observability.tsx) supplies the breadcrumb/document context, while Logs and Metrics lead with filters; Traces adds its own compact visible heading. Reuse the relevant sibling surface and review that heading variation before standardizing it.

## Layout

Start operational screens with [Page, PageHeader and Section](web/src/components/design/Page.tsx). They own the content measure, responsive side padding, section gap and header/action wrapping. Keep page scope visible before the content it controls. Resource region/time controls belong above the regional charts; observability resource/time controls belong above results.

Use space to distinguish levels, not to make every element a card. Section gaps separate tasks. Border dividers separate related rows inside one section. Use the shared [Table](web/src/components/design/Table.tsx) row/header spacing for comparable lists; retain the tighter dedicated trace geometry for telemetry. Do not enlarge every table row to match a settings form.

[SettingsRow](web/src/pages/settings/parts.tsx) gives labels and hints a stable column on desktop, limits the form measure, then stacks labels above fields on narrow screens. Forms need more vertical space than telemetry rows so hints, validation and pending feedback do not collide.

On narrow screens, let the document scroll and allow toolbars/actions to wrap. Choose a deliberate treatment for each data surface: deployment-history rows reflow into labeled groups, Traces reduces visible columns and uses a bottom Sheet for span details, and the dashboard resource table retains horizontal scrolling. Do not shrink text until every desktop column fits. Keep identity, state and the main action available; full technical values belong in copy/detail controls.

Resizable columns use [ColumnResizeHandle](web/src/components/design/ColumnResizeHandle.tsx) with [useColumnWidths](web/src/hooks/useColumnWidths.ts). Headers and rows share geometry. Preserve minimum bounds, keyboard controls, cancellation and distinct desktop/narrow preferences. Reuse the [Sheet](web/src/components/design/Sheet.tsx) and [Dialog](web/src/components/design/Dialog.tsx) wrappers for overlays rather than building a page-specific modal.

## Elevation & Depth

The dashboard, resource charts and settings use flat bordered sections on the page surface. Tonal headers and dividers establish grouping. Avoid adding a shadow to every static card: it competes with the data and makes dense screens harder to scan.

Reserve the existing `shadow-popover` and `shadow-drawer` treatments for transient menus and sheets. [DropdownMenu](web/src/components/design/DropdownMenu.tsx) owns popup geometry, surface, item highlight and shadow; the Sheet owns its edge and scrollable body. Maintain the same distinction in dark mode rather than adding brighter outlines around every layer.

## Shapes

Use the radius roles from [`index.css`](web/src/index.css) through their consumers: `rounded-lg` for section boundaries, `rounded-sm` for ordinary controls and badges. Follow [Button](web/src/components/design/Button.tsx) size variants instead of forcing one radius across small row actions and larger form actions. The established UI uses modest corners and thin separators; pill-shaped replacements and oversized icon containers change its character.

Use the existing Lucide icon set. Icons support a label, copy action or status; decorative icons should not displace operational text. Reuse shared motion and reduced-motion handling in `index.css`, including menu/sheet transitions. State changes must remain understandable without animation.

## Components

Application pages import from [`components/design`](web/src/components/design), whose wrappers adapt the vendored primitives in `components/ui`. Change an established wrapper when a system treatment needs to change; do not modify the vendored primitives or copy their classes into each page.

| Need | Canonical owner | Use |
| --- | --- | --- |
| Page/section hierarchy | [Page](web/src/components/design/Page.tsx) | Title, scope/actions, grouped content and section footer |
| Actions and fields | [Button](web/src/components/design/Button.tsx), [Input](web/src/components/design/Input.tsx), [Textarea](web/src/components/design/Textarea.tsx) | Primary/secondary/destructive emphasis; inherited field focus, disabled and invalid treatments |
| Tables and paging | [Table](web/src/components/design/Table.tsx), [Pager](web/src/components/design/Pager.tsx) | Aligned comparable rows and consistent page-size/navigation controls |
| Loading and empty content | [Skeleton](web/src/components/design/Skeleton.tsx), [EmptyState](web/src/components/design/EmptyState.tsx) | Keep loading geometry stable; explain an empty result and its next action |
| Overlays and feedback | [Dialog](web/src/components/design/Dialog.tsx), [Sheet](web/src/components/design/Sheet.tsx), [DropdownMenu](web/src/components/design/DropdownMenu.tsx), [error-handler](web/src/lib/error-handler.ts) | Focus/dismissal behavior, local recovery and safe user-facing error text |

### State and interaction review

- Preserve the section's position while loading. Replace the expected content with a matching Skeleton rather than showing an empty result first.
- Distinguish no data, no filter matches, unavailable capability, permission denied and request failure. The current Events unavailable card is not an example of zero recorded events. Give filtered-empty states a clear reset action.
- Keep existing rows visible when loading an older page fails, with a retry at the failed control. Describe incomplete results as loaded/partial; the Traces count and footer provide an existing example.
- Put validation beside the field and use its invalid treatment. The settings form's Name required message and Unsaved changes bar show different levels of feedback. Keep the draft on failure; pending submission must prevent duplicate mutation.
- Use SettingsRow's `controlId` and `hintId` for editable rows, with matching field IDs and `aria-describedby` references for hints and current validation errors. Read-only metadata keeps its text treatment. Use accessible names for icon-only controls. Preserve visible keyboard focus, Escape dismissal and focus return. A tooltip or colored border alone does not provide a field label or an error relationship.
- Show a disabled action's reason near the action when it matters. The workspace deletion card explains which resources must be removed first. Keep destructive actions separate and use the existing confirmation flow.
- Verify light/dark, narrow layout, long names, keyboard operation, loading, empty, error and retry behavior. Screenshots establish visual evidence; they do not certify accessibility or an API contract.

### Findings to review

These observations are not approved conventions for new screens:

| Observation | Evidence | Review direction |
| --- | --- | --- |
| Architecture can push the resource list below a long diagram and requires horizontal navigation on narrow screens | Sixteen-service dashboard capture; [ArchitectureDiagram](web/src/pages/dashboard/ArchitectureDiagram.tsx) | Review task priority and navigation at larger service counts; do not copy its height as a section-spacing rule |
| Visible page-heading treatment varies across observability and operational pages | [Observability](web/src/pages/Observability.tsx), [TracesView](web/src/pages/observability/TracesView.tsx), PageHeader | Preserve the compact telemetry workflow; clarify a shared heading convention before changing it |

### Review checklist

A new screen should have an identifiable task and scope, the appropriate existing page/section hierarchy, semantic theme tokens, shared wrappers, bounded technical text and legible narrow behavior. Its normal, pending, invalid, empty and failed states should have clear labels and recovery. Compare it with the relevant reference screen in both themes before approval.

### Feature contracts and source links

These are feature/API requirements, separate from the visual guidance above. Follow the maintained implementation and protocol when behavior changes:

- Async request identity: Connect Query/TanStack Query, scoped by resource and environment.
- Current regional deployment: `RegionStatus.deployment_id` in [resource.proto](proto/loco/resource/v1/resource.proto), independent of the history page.
- Deployment-history pagination: [deployment.proto](proto/loco/deployment/v1/deployment.proto) and [DeploymentsSection](web/src/pages/resource/DeploymentsSection.tsx).
- Trace layout, selection and span paging: [TracesView](web/src/pages/observability/TracesView.tsx), [TraceWaterfall](web/src/pages/observability/TraceWaterfall.tsx), [observability.proto](proto/loco/observability/v1/observability.proto).
- Metric and log navigation, formatting and scope: [MetricsView](web/src/pages/observability/MetricsView.tsx), [LogsView](web/src/pages/observability/LogsView.tsx) and [observability hooks](web/src/hooks).

The deployment-history cursor contract lives in `proto/loco/deployment/v1/deployment.proto`. Use Next and Previous consistently with the other tables. Until the final cursor is exhausted, describe the count as loaded records; fetch older pages on request. Keep typography, colors and control placement consistent with the resource page and the audit log.

The Traces view uses the shared observability shell, TimeRangeMenu and ResourceMenu. Committed trace ID, resource filter, span selection and fixed time bounds live in URL search parameters. Span pages keep the same bounds and regional API transport. Browse counts describe loaded spans; trace detail expands the selection to all resources in the current workspace and environment. The waterfall uses compact single-line rows, one timing unit across its axis, stable service colors and separate semantic error markers. The ruler and rows share a scroller to keep their timing columns aligned. Span details sit below the waterfall on desktop, with searchable Attributes, Resource and Instrumentation tabs. Narrow screens preserve single-line operation/timing rows and open span details in the shared bottom Sheet with Escape and focus recovery.

Log message previews and details remove terminal color and style sequences before highlighting text. The JSON record and raw-message copy preserve the original log body.

The Metrics view opens with the collected CPU and memory charts below the resource and time filters. Keep planned metrics and inactive controls out of the chart area; preserve loading, empty and error states for the collected metrics.

Metric charts keep timeline event lines free of overlapping text. Their event menu lists the full event titles and times, newest first, and opens logs at the selected event. Use the shared DropdownMenu for keyboard navigation, dismissal and focus recovery.

Metric-to-log links include the selected timestamp (`at`), time range and resource selection in the URL. Focused log controls describe a time window around that timestamp; the range remains in the URL when it changes. Focused queries load records on both sides of that timestamp with disjoint bounds; Older and Newer retain those bounds while following directional cursors. Return to latest and Live leave the focused window.

Application compute charts query the selected environment and wait for it to load. Build-job telemetry has no runtime environment and stays outside these charts. Changing the environment uses a separate metrics cache.

Historical logs and live tail use the selected environment. Queries wait for that selection; changing it resets pagination and log selection, uses separate cached history, and closes the previous live stream. Build logs remain available through unfiltered API queries.

Shared buttons and segmented controls use the compact 28px default height from their design wrappers. Single-select segmented controls slide one surface between selections; multiple-select controls keep independent surfaces. Motion follows the system reduced-motion preference. The top scope and search bar stays at the top of document scrolling with an opaque background.
