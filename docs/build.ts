import { $, Glob, file, write } from "bun";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `bun build.ts pages` builds docs.loco.build into site/: the newest v* tag at /
// and /<tag>/, the tag before it at /<tag>/, and this checkout at /staging/.
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

// Builds the docs of the checkout at root for the router basename base into output.
async function build(
  root: string,
  base: string,
  url: string,
  environment: string,
  output: string,
  version = "",
) {
  const dir = join(root, "docs");
  await $`${process.execPath} --bun run react-router build`.cwd(dir).env({
    ...process.env,
    VITE_DOCS_URL: url,
    VITE_DOCS_ENVIRONMENT: environment,
    VITE_DOCS_VERSION: version,
    DOCS_BASE_PATH: base,
  });
  await rm(output, { recursive: true, force: true });
  await cp(join(dir, "build/client"), output, { recursive: true });
  if (base !== "/") {
    await cp(join(output, base), output, { recursive: true });
    await rm(join(output, base), { recursive: true });
  }
}

if (target === "pages") {
  const site = join(docs, "site");
  const tags = (await $`git tag --list ${"v*"} --sort=-v:refname`.cwd(docs).text())
    .split("\n")
    .filter(Boolean)
    .slice(0, 2);
  const versions: { name: string; url: string }[] = [];
  // A tag whose docs fail to build is left out; the next tag becomes current.
  for (const tag of tags) {
    const checkout = await mkdtemp(join(tmpdir(), `loco-docs-${tag}-`));
    try {
      await $`git worktree add --detach ${checkout} ${tag}`.cwd(docs);
      await $`bun install --frozen-lockfile`.cwd(checkout);
      const current = versions.length === 0;
      const prefix = `/${tag}/`;
      if (current) await build(checkout, "/", pages, "production", site, tag);
      await build(
        checkout,
        prefix,
        new URL(prefix, pages).href,
        "production",
        join(site, tag),
        tag,
      );
      versions.push({ name: tag, url: current ? "/" : prefix });
    } catch (error) {
      console.warn(`Skipping the docs of ${tag}: ${error}`);
    } finally {
      await $`git worktree remove --force ${checkout}`.cwd(docs).nothrow();
    }
  }
  assert(versions.length > 0, `No release docs built from ${tags.join(", ")}`);
  await build(
    repository,
    "/staging/",
    new URL("staging/", pages).href,
    "staging",
    join(site, "staging"),
    "staging",
  );
  versions.push({ name: "staging", url: "/staging/" });
  await cp(join(site, "404/index.html"), join(site, "404.html"));
  await write(join(site, "CNAME"), `${new URL(pages).hostname}\n`);
  await write(join(site, "versions.json"), JSON.stringify(versions));
  process.exit();
}

const mount = join(docs, "site-mounted");
await build(
  repository,
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
