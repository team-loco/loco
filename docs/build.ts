import { $, Glob, file, write } from "bun";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, rm } from "node:fs/promises";
import { join } from "node:path";

// `bun build.ts pages` builds the docs.loco.build site into site/.
// `bun build.ts dashboard <production|staging>` builds the dashboard's /docs/ mount
// into site-mounted/ and writes its Static Web Server configuration to .sws.toml.
const docs = import.meta.dir;
const [target, environment = "production"] = process.argv.slice(2);
assert(target === "pages" || target === "dashboard", `Unknown target: ${target}`);
assert(
  environment === "production" || environment === "staging",
  `Unknown environment: ${environment}`,
);
const url =
  environment === "production"
    ? "https://docs.loco.build/"
    : "https://staging.loco.build/docs/";
const base = target === "pages" ? "/" : "/docs/";
const site = join(docs, target === "pages" ? "site" : "site-mounted");

await $`${process.execPath} --bun run react-router build`.cwd(docs).env({
  ...process.env,
  VITE_DOCS_URL: url,
  VITE_DOCS_ENVIRONMENT: environment,
  DOCS_BASE_PATH: base,
});
await rm(site, { recursive: true, force: true });
await cp(join(docs, "build/client"), site, { recursive: true });

if (target === "pages") {
  assert.equal(environment, "production", "Pages serves production only");
  await cp(join(site, "404/index.html"), join(site, "404.html"));
  await write(join(site, "CNAME"), `${new URL(url).hostname}\n`);
  process.exit();
}

await cp(join(site, "docs"), site, { recursive: true });
await rm(join(site, "docs"), { recursive: true });

// Static Web Server needs the hashes of the mount's inline scripts.
const hashes = new Set<string>();
for await (const page of new Glob("**/*.html").scan({
  cwd: site,
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
let config = await file(join(docs, "../web/sws.toml")).text();
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
