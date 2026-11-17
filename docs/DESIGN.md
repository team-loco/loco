---
version: alpha
name: Loco documentation
description: Loco product typography and theme adapted for a technical reading experience.
colors:
  background: "#ffffff"
  surface: "#f5f5f4"
  raised: "#ececea"
  foreground: "#18181b"
  secondary: "#45454a"
  border: "#ebebe9"
  strong-border: "#d8d8d5"
  primary: "#2455eb"
  link: "#2455eb"
  logo: "#2455eb"
  dark-background: "#0f0f11"
  dark-surface: "#161618"
  dark-raised: "#202023"
  dark-foreground: "#ececef"
  dark-secondary: "#b6b6bd"
  dark-border: "#232327"
  dark-strong-border: "#313137"
  dark-primary: "#7b9cff"
  dark-link: "#7b9cff"
  dark-logo: "#7b9cff"
typography:
  display:
    fontFamily: '"Geist Variable", sans-serif'
  sans:
    fontFamily: '"Geist Variable", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
  mono:
    fontFamily: '"Geist Mono Variable", ui-monospace, monospace'
rounded:
  DEFAULT: "6px"
  sm: "4px"
spacing:
  page-max: "100%"
  section-gap: "2rem"
components:
  navigation: {}
  search: {}
  button: {}
  code: {}
  table: {}
---

# Loco documentation design

## Overview

The docs are a technical field guide for developers deploying applications and operators running Loco. The signature is the small handwritten Loco wordmark aligned with a compact documentation header, using the dashboard’s fonts, neutral surfaces, and blue accents.

Content is English, for the project's developer audience. The user brief defines three deployment modes; the main branch supplies released commands; the infrastructure PR stack supplies the explicitly labeled Go preview. Technical accuracy and readable command examples take precedence over promotional copy.

`web/src/theme.css` owns the Loco palette and font-family tokens. `app/app.css` imports that source and maps its variables to Fumadocs’ `--color-fd-*` tokens; it owns the documentation reading scale and layout. The favicon comes from `web/public/favicon.svg`, and the wordmark comes from `web/public/logo.svg`; Vite imports bundle both assets with content hashes. `web/src/fonts.css` supplies the same locally hosted Geist and Geist Mono font faces to both products. The documentation shares these CSS sources without importing the dashboard JavaScript. Keep both light and dark palettes coherent with the dashboard. Avoid generic gradients, animated marketing blocks, and tile grids.

## Colors

Fumadocs background, card, muted, foreground, muted-foreground, border, and primary map to Loco’s `--bg`, `--panel`, `--panel2`, `--fg`, `--fg3`, `--line`, and `--brand`. The sidebar uses `--shell`. The SVG wordmark is masked with `--logo`, matching the dashboard and following dark mode. Scrollbars use `--line2` on `--panel`; forced colors use system colors.

## Typography

Geist handles headings, body, and navigation; Geist Mono handles commands. Fonts are hosted locally. Reading text uses 16px with a 1.7 line height; navigation uses 13px with a 1.5 line height. Page titles use 32px, weight 600, line height 1.2, and tracking -0.035em. Section titles use 23px with a 1.3 line height and tracking -0.025em. On narrow screens page titles use 28px and section titles 21px.

## Layout

One UI container serves the docs at their canonical hostname and at the dashboard’s `/docs/` path. Separate prerendered builds with router basenames `/` and `/docs/` preserve the active entry point. The full-width grid contains a 64px top header, a 244px desktop navigation rail, an article up to 760px including 32px side padding, and a 200px table of contents on wide screens. On narrow screens article padding is 20px. Sections expand by default. Neither scrolling nor changing pages hides desktop navigation. Below the upstream drawer breakpoint, Fumadocs owns the mobile navigation drawer. Tables and code scroll horizontally within their content containers.

Track unfinished work in the owning repository’s GitHub issues. Do not maintain parallel HTML notes or checklists.

## Elevation & Depth

Neutral surface tones and thin borders distinguish code, tables, and search. The header has a bottom border and no shadow. Fumadocs owns search overlays and their stacking behavior.

## Shapes

Controls and code use a 6px radius. Navigation uses 4px. Blue marks navigation and links; the rest of the article uses the product’s neutral surfaces.

## Components

Fumadocs owns navigation, search, theme switching, page actions, code-copy feedback, and mobile drawer behavior. The notebook layout supplies the top header and responsive navigation. Custom styling adapts those owners instead of replacing their event handling. Links have hover color and visible keyboard focus. Primary and secondary actions use the same geometry. The staging notice uses an in-flow tinted callout with an information icon and blue border, linking to production documentation without covering navigation or content. The top header links to the API reference for the current documentation environment and its dashboard. API and dashboard links carry external-link arrows. The theme control uses small rectangular light, dark, and system segments. View Markdown is a small page-title utility.

The table of contents is visible on wide screens without hover or a toggle. Scrollbars have global track/thumb tokens and remain operable. Reduced motion disables animation and transitions. Documentation uses sentence case and real commands; unreleased interfaces carry a visible notice that also appears in Markdown exports.

## Do's and Don'ts

- Keep the full desktop navigation available on every page.
- Verify light, dark, and narrow layouts after theme changes.
- Keep content inside `content/` so internal notes cannot enter the public build.
- Do not add page front matter that hides navigation or external AI chat services.
