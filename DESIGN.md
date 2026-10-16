---
version: alpha
name: Loco
description: A compact operations dashboard for developers deploying services.
omitted:
  - section: colors
    reason: web/src/index.css owns theme values and semantic colors.
  - section: typography
    reason: web/src/index.css owns font families and the type scale.
  - section: spacing
    reason: Existing shared design components own layout spacing.
  - section: rounded
    reason: web/src/index.css owns radius tokens.
  - section: shadows
    reason: web/src/index.css owns elevation tokens.
---

## Product context

Loco's dashboard helps developers inspect deployment state, compare releases and operate services. The existing English UI uses compact text, tabular numbers, subdued borders and inline telemetry charts. Preserve the current operations-console layout in light and dark themes; use document scrolling on narrow screens.

## Runtime ownership

`web/src/index.css` is the canonical token source. Its CSS variables feed Tailwind through `@theme inline`; design wrappers consume those semantic classes. This document does not generate tokens. `web/src/components/ui/` remains vendored, and application components import wrappers from `web/src/components/design/`.

## Shared behavior

| Capability | Owner and contract |
| --- | --- |
| Page and section layout | `web/src/components/design/Page.tsx` |
| Pagination and row count | `web/src/components/design/Pager.tsx`; cursors come from the RPC response |
| Row-count selection | The Pager's shared ToggleGroup |
| Async data and request identity | Connect Query and TanStack Query; scope queries by resource and environment |
| Current regional deployment | `RegionStatus.deployment_id` in `proto/loco/resource/v1/resource.proto`, independent of the history page |
| User-facing errors | `web/src/lib/error-handler.ts`; preserve displayed history when an older page fails |
| Dialogs and confirmations | Shared design Dialog wrappers; existing `AGENTS.md` frontend rules apply |

The deployment-history cursor contract lives in `proto/loco/deployment/v1/deployment.proto`. Use Next and Previous consistently with the other tables. Until the final cursor is exhausted, describe the count as loaded records; fetch older pages on request. Keep typography, colors and control placement consistent with the resource page and the audit log.

The Traces view uses the shared observability shell, TimeRangeMenu and ResourceMenu. Committed trace ID, resource filter, span selection and fixed time bounds live in URL search parameters. Span pages keep the same bounds and regional API transport. Browse counts describe loaded spans; trace detail expands the selection to all resources in the current workspace and environment. The waterfall uses compact single-line rows, one timing unit across its axis, stable service colors and separate semantic error markers. The ruler and rows share a scroller to keep their timing columns aligned. Span details sit below the waterfall on desktop, with searchable Attributes, Resource and Instrumentation tabs. Narrow screens preserve single-line operation/timing rows and open span details in the shared bottom Sheet with Escape and focus recovery.

Log message previews and details remove terminal color and style sequences before highlighting text. The JSON record and raw-message copy preserve the original log body.

The Metrics view opens with the collected CPU and memory charts below the resource and time filters. Keep planned metrics and inactive controls out of the chart area; preserve loading, empty and error states for the collected metrics.

Metric charts keep timeline event lines free of overlapping text. Their event menu lists the full event titles and times, newest first, and opens logs at the selected event. Use the shared DropdownMenu for keyboard navigation, dismissal and focus recovery.
