---
version: alpha
name: Loco documentation
description: A technical field guide using Loco's blue mark and warm neutral surfaces.
colors:
  background: "#ffffff"
  surface: "#faf9f8"
  raised: "#f1efed"
  foreground: "#2a2522"
  secondary: "#56504a"
  border: "#e8e5e1"
  strong-border: "#d3cfc9"
  primary: "#1e40af"
  link: "#2c4a99"
  logo: "#2455eb"
  dark-background: "#161412"
  dark-surface: "#1c1a17"
  dark-raised: "#272420"
  dark-foreground: "#f3efea"
  dark-secondary: "#c9c2ba"
  dark-border: "#2f2b27"
  dark-strong-border: "#403b35"
  dark-primary: "#4a74d6"
  dark-link: "#8aa6e6"
  dark-logo: "#6f8ff2"
typography:
  display:
    fontFamily: '"Avenir Next", "Segoe UI", sans-serif'
  sans:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "Helvetica Neue", Arial, sans-serif'
  mono:
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace'
rounded:
  DEFAULT: "6px"
  sm: "4px"
spacing:
  page-max: "76rem"
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

The docs are a technical field guide for developers deploying applications and operators running Loco. The signature is the existing handwritten Loco wordmark beside a quiet documentation shell, with a blue rule marking the terminal-to-Kubernetes introduction.

Content is English, for the project's developer audience. The user brief defines three deployment modes; the main branch supplies released commands; the infrastructure PR stack supplies the explicitly labeled Go preview. Technical accuracy and readable command examples take precedence over promotional copy.

`content/assets/loco.css` owns runtime tokens and maps them to Zensical's `--md-*` variables. This file records that mapping. The favicon comes from `web/public/favicon.svg`, and the inline wordmark comes from `web/src/components/design/logo-strokes.ts` during each build. Generated assets stay out of Git. Colors mirror `web/src/index.css`; the site is independent of the dashboard bundle. Keep both light and dark palettes coherent with the dashboard. Avoid generic gradients, animated marketing blocks, and tile grids.

## Colors

Background, surface, raised, foreground, secondary, border, strong-border, primary, link, and logo map to `--bg`, `--bg2`, `--bg3`, `--fg`, `--fg2`, `--line`, `--line2`, `--accent`, `--link`, and `--logo`. Dark variants override the same tokens under Zensical's slate scheme. Primary buttons use white text in light mode and the dark background token in dark mode. Keyboard focus uses the link token. Forced colors preserve system scrollbars and button borders.

## Typography

Avenir Next with Segoe UI fallback gives headings a distinct display face. Body text uses the dashboard's system family at 16px, with a 1.7 line height. Code uses system monospace. No third-party font requests are required. Headings use restrained negative tracking; navigation labels remain smaller than content.

## Layout

One UI container serves the docs at their canonical hostname and at the dashboard’s `/docs/` path. Relative navigation preserves the active entry point. The 76rem grid contains desktop navigation, article, and a table of contents on wide screens. Sections expand by default. Neither scrolling nor changing pages hides desktop navigation. Below the upstream drawer breakpoint, Zensical owns the mobile navigation drawer. Tables and code scroll horizontally within their content containers.

The internal trackers, `notes.html` and `checklist.html`, are standalone files outside `content/`. Each embeds the same light/dark tokens, system fonts, section navigation and print styles so it can be opened directly from disk. The checklist shows open items; completion is recorded by editing the file.

## Elevation & Depth

Warm surface tones and thin borders distinguish code, tables, and search. The header has a bottom border and no shadow. Zensical owns search overlays and their stacking behavior.

## Shapes

Controls and code use a 6px radius. Navigation uses 4px. The blue introduction rule carries the expressive accent; the rest of the article retains standard documentation geometry.

## Components

Zensical owns navigation, search, theme switching, page actions, code-copy feedback, and mobile drawer behavior. Custom styling adapts those owners instead of replacing their event handling. Links have hover color and visible keyboard focus. Primary and secondary actions use the same geometry. The staging banner remains visible and links to production documentation.

The table of contents is visible on wide screens without hover or a toggle. Scrollbars have global track/thumb tokens and remain operable. Reduced motion disables animation and transitions. Documentation uses sentence case and real commands; unreleased interfaces carry a visible notice that also appears in Markdown exports.

## Do's and Don'ts

- Keep the full desktop navigation available on every page.
- Verify light, dark, and narrow layouts after theme changes.
- Keep content inside `content/` so internal notes cannot enter the public build.
- Do not add page front matter that hides navigation or external AI chat services.
