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
bun run --cwd docs typecheck
mise run test:hosting
```

Docs CI typechecks and builds the Pages site and the staging dashboard mount. Checks of the UI image, its `/docs/` mount and the Pages build live in `tests/hosting/`, alongside the platform tests rather than the documentation. The advisory Docs Drift workflow reports code changes that leave these pages stale; its rubric is `.agents/skills/docs-drift/SKILL.md`.

Bun dependencies are pinned in `docs/package.json` and the root `bun.lock`. The docs build needs only Bun. Python dependencies for shared platform tests are pinned in `tests/uv.lock`. Run `bun install --frozen-lockfile` from the repository root before building.

## Theme and navigation

`docs/app/app.css` maps the dashboard palette in `web/src/theme.css` to Fumadocs theme tokens; `docs/DESIGN.md` records the reading scale and layout. Fumadocs owns the sidebar, mobile drawer, search dialog, theme switching, table of contents, and code-copy controls. Desktop sections expand by default. The dashboard and docs share locally hosted Geist and Geist Mono font faces through `web/src/fonts.css`, with no third-party font requests.

The site imports its favicon and wordmark from `web/public/`; Vite bundles them with the correct base path and content hashes. Edit the web sources to update both surfaces. The staging build includes a visible banner and crawler restrictions.

## Hosting

The site is published in two places. GitHub Pages serves `docs.loco.build`, the canonical hostname. The `web/Dockerfile` image serves the docs of its own commit under `/docs/` on the dashboard hostname: `loco.build/docs/` and `staging.loco.build/docs/`.

`docs.loco.build` holds three versions:

| Path | Version |
| --- | --- |
| `/` and `/<tag>/` | The newest `v*` tag |
| `/<tag>/` | The tag before it |
| `/staging/` | `main`, with the staging banner and `noindex` |

`mise run docs:build` builds them into `docs/site/`. It extracts each tag's `docs/content` with `git archive` and renders every version with the current checkout's docs app, under the router basename of its path, so all versions share the same layout, version menu and search. Each version has its own search index, `llms.txt`, `sitemap.xml` and canonical URLs. The root adds `404.html`, which answers every missing path, `robots.txt`, the only one crawlers read, and `versions.json`, the list the version menu in the docs header reads at run time. The build fails if any version fails, and leaves `docs/site/` untouched.

The Documentation Pages workflow, `.github/workflows/docs-pages.yml`, runs that build from `main` and deploys `docs/site/` on every push to `main` that changes the documentation, after every successful Release run, on every published release and on manual dispatch. A failed build deploys nothing, and Pages keeps serving the last deploy. Pages serves the files without custom headers, so `docs.loco.build` has no Content-Security-Policy and Pages chooses each file's `Content-Type` from its extension. The custom domain is set in the repository's Pages settings.

The image runs `bun build.ts dashboard <environment>`, which builds the mount into `docs/site-mounted/` with router basename `/docs/` and generates the image's Static Web Server configuration with exact hashes for the mount's inline scripts. The policy does not allow arbitrary inline scripts. Generated output in `docs/site/`, `docs/site-mounted/` and `.sws.toml` stays out of Git.

Vite serves and copies raw Markdown through `docs/public`, a symlink to `content/`, retaining source paths and frontmatter, including `/skills.md`. Fumadocs generates `llms.txt` and `llms-full.txt` through its `llms()` API; React Router prerenders these resource routes alongside `sitemap.xml`, `robots.txt` and the search index at `/api/search`. The UI image serves the deployment skill at `/skills.md` and `/docs/skills.md` on the dashboard hostname, along with the CLI install script at `/install.sh` and the loco.yaml JSON Schema at `/schemas/loco.v1.json`.

`web/sws.toml` defines the image's static root and dashboard rewrites. Missing documentation URLs return HTTP 404; unknown dashboard paths open the dashboard.
