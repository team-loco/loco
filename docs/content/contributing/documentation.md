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

The `docs/Dockerfile` image serves the static build with the same pinned `static-web-server` image as the dashboard on port 8080. The Railway `loco::cp-docs` service uses a commit-tagged image, with a separate staging image to generate the correct canonical URLs and indexing rules.

Main merges build both images and deploy staging. The production workflow promotes the selected commit, matching the API and dashboard release. Production uses `docs.loco.build`; staging uses `docs.staging.loco.build`.

On the first deployment, add the CNAME and domain verification records supplied by Railway for each custom domain to the DNS provider. The repository declares the Railway domains; DNS targets are assigned when Railway creates them. Wait for domain verification and HTTPS before treating the site as live.

Inspect a [Railway configuration plan](https://github.com/team-loco/loco/actions/workflows/railway-config.yml) for each environment before merging infrastructure changes.
