---
version: alpha
omitted:
  - section: colors
    reason: Runtime CSS variables in web/src/index.css own the light and dark palettes.
  - section: typography
    reason: Runtime font and type scale tokens in web/src/index.css are canonical.
  - section: spacing
    reason: Existing Tailwind spacing and design wrappers own layout rhythm.
  - section: rounded
    reason: Runtime radius tokens in web/src/index.css are canonical.
  - section: components
    reason: web/src/components/design implements component variants.
---

# Loco product design

## Overview

A deployment console for developers operating applications and reviewing changes. Preserve the compact operational interface: workspace navigation, environment selection, architecture, status and service details. Infrastructure ownership belongs beside a service’s status so an operator can identify the defining stack.

## Colors

`web/src/index.css` is the source of truth. CSS variables map through `@theme inline` into semantic Tailwind tokens. Use `bg-bg2`, `bg-bg3`, `text-fg2`, `text-fg3` and `border-line`; success, warning and failure use the `ok`, `warn` and `bad` pairs. Light and dark themes preserve the same meaning. Do not copy palette values into pages.

## Typography

System sans typography supports dense operational controls; system monospace identifies IDs, images and code. Runtime type tokens use a 13px body with 1.4 line height, 12px small copy and 24px major headings. Use existing wrappers rather than introducing a feature-specific type scale.

## Layout

The app shell owns navigation and responsive behavior. Page and Section own content spacing. Service creation uses the existing dialog and drawer. Preserve URL environment selection and retain actions at narrow viewports. Read-only provenance badges wrap within their owning header.

## Elevation & Depth

Use existing popover and drawer shadows. Keep normal content surfaces quiet; status and actionable errors carry semantic emphasis.

## Shapes

Use the existing radius tokens and design wrappers. Do not change the shape language for infrastructure features.

## Components

`web/src/components/design` owns application variants; `web/src/components/ui` is vendored. Button, Field, Input, Dialog, Badge, ToggleGroup and Sonner are the canonical owners. No new equivalent controls are required for infrastructure ownership.

## Do's and Don'ts

Use English sentence case and explicit action names. Preserve entered values on request failure and use the Connect error handler. Use real buttons and links with visible keyboard focus. Pointer behavior comes from global CSS. Avoid decorative deployment metaphors and additional marketing copy in operational flows.
