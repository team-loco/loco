import { $, Glob, file, write } from "bun";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, realpath, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `bun build.ts pages` builds docs.loco.build into site/: the content of the
// newest v* tag at / and /<tag>/, of the tag before it at /<tag>/, and of this
// checkout at /staging/, all rendered by this checkout.
// `bun build.ts dashboard <production|staging>` builds the dashboard's /docs/
// mount into site-mounted/ and writes its Static Web Server configuration to
// .sws.toml.
const docs = import.meta.dir;
const repository = join(docs, "..");
const pages = "https://docs.loco.build/";
const [target, environment = "production"] = process.argv.slice(2);
assert(target === "pages" || target === "dashboard", `Unknown target: ${target}`);
assert(
  environment === "production" || environment === "staging",
  `Unknown environment: ${environment}`,
);

// Renders the docs app in app for the router basename base into output.
async function build(
  app: string,
  base: string,
  url: string,
  environment: string,
  output: string,
  version = "",
) {
  await $`${process.execPath} --bun run react-router build`.cwd(app).env({
    ...process.env,
    VITE_DOCS_URL: url,
    VITE_DOCS_ENVIRONMENT: environment,
    VITE_DOCS_VERSION: version,
    DOCS_BASE_PATH: base,
  });
  await rm(output, { recursive: true, force: true });
  await cp(join(app, "build/client"), output, { recursive: true });
  if (base !== "/") {
    await cp(join(output, base), output, { recursive: true });
    await rm(join(output, base), { recursive: true });
  }
}

if (target === "pages") {
  const tags = (await $`git tag --list ${"v*"} --sort=-v:refname`.cwd(repository).text())
    .split("\n")
    .filter(Boolean)
    .slice(0, 2);
  assert.equal(tags.length, 2, "Pages needs two v* tags");
  // The site is assembled in scratch, so a failed build leaves site/ as it was.
  const scratch = await realpath(await mkdtemp(join(tmpdir(), "loco-docs-")));
  const site = join(scratch, "site");
  // Each tag's content is rendered by a copy of this docs app, beside links to
  // the repository's web/ and dependencies.
  const apps = [];
  for (const tag of tags) {
    const root = join(scratch, tag);
    const app = join(root, "docs");
    await mkdir(join(app, "content"), { recursive: true });
    for (const name of ["web", "node_modules"]) {
      await symlink(join(repository, name), join(root, name));
    }
    await symlink(join(docs, "node_modules"), join(app, "node_modules"));
    for (const name of [
      "app",
      "public",
      "package.json",
      "tsconfig.json",
      "vite.config.ts",
      "react-router.config.ts",
    ]) {
      await cp(join(docs, name), join(app, name), {
        recursive: true,
        verbatimSymlinks: true,
      });
    }
    await $`git archive ${`${tag}:docs/content`} | tar -x -C ${join(app, "content")}`.cwd(
      repository,
    );
    apps.push(app);
  }
  const [current, previous] = tags;
  await build(apps[0], "/", pages, "production", site, current);
  for (const [i, tag] of tags.entries()) {
    const prefix = `/${tag}/`;
    const url = new URL(prefix, pages).href;
    await build(apps[i], prefix, url, "production", join(site, tag), tag);
  }
  await build(
    docs,
    "/staging/",
    new URL("staging/", pages).href,
    "staging",
    join(site, "staging"),
    "staging",
  );
  await cp(join(site, "404/index.html"), join(site, "404.html"));
  await write(
    join(site, "versions.json"),
    JSON.stringify([
      { name: current, url: "/" },
      { name: previous, url: `/${previous}/` },
      { name: "staging", url: "/staging/" },
    ]),
  );
  await rm(join(docs, "site"), { recursive: true, force: true });
  await cp(site, join(docs, "site"), { recursive: true });
  await rm(scratch, { recursive: true });
  process.exit();
}

const mount = join(docs, "site-mounted");
await build(
  docs,
  "/docs/",
  environment === "production" ? pages : new URL("staging/", pages).href,
  environment,
  mount,
);

// Static Web Server needs the hashes of the mount's inline scripts.
const hashes = new Set<string>();
for await (const page of new Glob("**/*.html").scan({
  cwd: mount,
  absolute: true,
})) {
  let chunks: string[] = [];
  await new HTMLRewriter()
    .on("script:not([src])", {
      element(element) {
        chunks = [];
        element.onEndTag(() => {
          hashes.add(
            `'sha256-${createHash("sha256").update(chunks.join("")).digest("base64")}'`,
          );
        });
      },
      text(text) {
        chunks.push(text.text);
      },
    })
    .transform(new Response(file(page)))
    .text();
}
assert(hashes.size > 0, "Documentation has no inline scripts");
let config = await file(join(repository, "web/sws.toml")).text();
assert.equal(
  config.split("script-src 'self';").length,
  2,
  "UI must have one script policy",
);
config = config.replace(
  "script-src 'self';",
  `script-src 'self' ${Array.from(hashes).sort().join(" ")};`,
);
// An image built for an API on another origin must be allowed to call it.
const apiURL = process.env.PUBLIC_API_URL ?? "";
if (URL.canParse(apiURL)) {
  assert.equal(
    config.split("connect-src 'self';").length,
    2,
    "UI must have one connect policy",
  );
  config = config.replace(
    "connect-src 'self';",
    `connect-src 'self' ${new URL(apiURL).origin};`,
  );
}
await write(join(docs, ".sws.toml"), config);
