---
title: "Documentation maintenance"
---

The documentation site uses Fumadocs with React Router and Vite in the `docs/` Bun workspace. Published Markdown lives in `docs/content/`; internal notes outside that directory stay outside the site.

```sh
mise run docs:serve
```

The preview listens at `http://127.0.0.1:8001` and reloads when content or styles change.

## Edit and verify

Add Markdown pages under `docs/content/` with a `title` in YAML frontmatter. Register their order in the folder's `meta.json`. Use relative `.md` links between articles; the renderer resolves them to the corresponding page at either hosting path. Use fenced code blocks, tables, and blockquotes for notices.

```sh
mise run docs:build
mise run docs:build:staging
mise run test:docs
mise run docs:check:container
```

The tests check production and staging metadata, exported Markdown, branding, and local links. The container tests exercise navigation, search, themes, and resource loading under the deployed content security policy. The advisory Docs Drift workflow reports code changes that leave these pages stale; its rubric is `.agents/skills/docs-drift/SKILL.md`.

Bun dependencies are pinned in `docs/package.json` and the root `bun.lock`. Python dependencies for the build wrapper and browser tests are pinned in `docs/uv.lock`. Run `bun install --frozen-lockfile` from the repository root before building.

## Theme and navigation

`docs/app/app.css` maps the palette in `docs/DESIGN.md` to Fumadocs theme tokens. Fumadocs owns the sidebar, mobile drawer, search dialog, theme switching, table of contents, and code-copy controls. Desktop sections expand by default. System fonts keep the site independent of third-party font requests.

The build copies the favicon and wordmark from `web/public/` into ignored `docs/public/assets/`. Edit the web sources to update both surfaces. The staging build includes a visible banner and crawler restrictions.

## Hosting

The `web/Dockerfile` image packages the dashboard and documentation together. One Static Web Server process serves the dashboard, docs at `/docs/`, the loco.yaml JSON Schema at `/schemas/loco.v1.json`, and the docs hostname through a virtual host.

Production serves `docs.loco.build` and `loco.build/docs/`. Staging serves `docs.staging.loco.build` and `staging.loco.build/docs/`. The docs hostname is canonical. The build prerenders every article twice, with router basenames `/` and `/docs/`, so navigation and assets work at both entry points without a runtime JavaScript server. Generated output goes in `docs/site/` and `docs/site-mounted/` and stays out of Git.

Markdown exports retain their source paths and frontmatter, including `/skills.md`. Both builds also publish `llms.txt`, `llms-full.txt`, `sitemap.xml`, and `robots.txt`. The UI image serves the deployment skill at `/skills.md` and `/docs/skills.md` on the dashboard hostname.

`web/sws.toml` defines static roots and dashboard rewrites. Missing documentation URLs return HTTP 404; unknown dashboard paths open the dashboard. `docs/scripts/package.py` generates the server configuration with exact hashes for inline scripts in both prerendered builds. The policy does not allow arbitrary inline scripts. Generated `.sws.toml` stays outside the public site and out of Git.
