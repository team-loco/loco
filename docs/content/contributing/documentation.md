# Documentation maintenance

The documentation site is a Zensical project contained in `docs/`. Published Markdown lives in `docs/content/`; older notes and design documents outside that directory stay outside the site.

```sh
mise run docs:serve
```

The preview listens at `http://127.0.0.1:8001`. Zensical rebuilds when content changes. Restart the preview after changing the theme configuration.

## Edit and verify

Add pages under `docs/content/` and register them in `docs/zensical.toml`. Keep page names and headings consistent with the CLI and API. Describe released behavior, and label unreleased workflows with a release notice.

```sh
mise run docs:build
mise run docs:build:staging
mise run test:docs
uv run --frozen --project docs playwright install chromium
mise run docs:check:browser
mise run docs:check:container
```

Builds run in strict mode and fail on warnings. Generated output goes in `docs/site/` and stays out of Git. Python dependencies are pinned in `docs/uv.lock`; the runner and Python versions are pinned by mise.

## Theme and navigation

`docs/content/assets/loco.css` owns the documentation tokens and theme. `docs/overrides/main.html` adds staging metadata and its banner. `docs/DESIGN.md` records the visual contract. The build copies the favicon from `web/public/favicon.svg` and generates the wordmark from `web/src/components/design/logo-strokes.ts`. The generated favicon and `docs/overrides/partials/logo.html` stay ignored; edit the web sources to update both surfaces.

Desktop navigation remains visible as you scroll, with every section expanded. The table of contents also remains visible on wide screens. Narrow viewports use Zensical's navigation drawer. Do not enable navigation pruning, navigation tabs, header auto-hide, or page metadata that hides navigation.

## Hosting

The `web/Dockerfile` image packages the dashboard and documentation together. One Static Web Server process serves the dashboard, docs at `/docs/`, and the docs hostname through a virtual host. The UI service owns both domains; documentation adds no service or image publication job.

Production serves `docs.loco.build` and `loco.build/docs/`. Staging serves `docs.staging.loco.build` and `staging.loco.build/docs/`. The docs hostname is canonical; links inside the rendered site are relative so navigation also works under `/docs/`.

Main merges build production and staging UI images containing their matching docs builds, then notify the repository named by the `DEPLOY_DISPATCH_REPOSITORY` variable, which deploys them.

`web/sws.toml` defines static roots and dashboard route rewrites. Existing dashboard routes resolve to the UI entrypoint; missing documentation URLs return HTTP 404. Add a rewrite when introducing a new dashboard route prefix. The container tests check the routes declared in `web/src/App.tsx`.

`docs/scripts/package.py` generates the server configuration with exact CSP hashes for Zensical's inline scripts. The dashboard keeps its existing script restrictions. Generated `.sws.toml` stays outside the public site and out of Git.
