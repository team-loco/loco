import { $, Glob, file, write } from "bun";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, readdir, rm } from "node:fs/promises";
import { join } from "node:path";

const docs = import.meta.dir;
const environment = process.argv[2] ?? "production";
assert(
  environment === "production" || environment === "staging",
  `Unknown environment: ${environment}`,
);
const url =
  environment === "production"
    ? "https://docs.loco.build/"
    : "https://docs.staging.loco.build/";
const env = {
  ...process.env,
  VITE_DOCS_URL: url,
  VITE_DOCS_ENVIRONMENT: environment,
};

for (const [base, output] of [
  ["/", "site"],
  ["/docs/", "site-mounted"],
]) {
  await $`${process.execPath} --bun run react-router build`
    .cwd(docs)
    .env({ ...env, DOCS_BASE_PATH: base });
  const site = join(docs, output);
  await rm(site, { recursive: true, force: true });
  await cp(join(docs, "build/client"), site, { recursive: true });
  if (base !== "/") {
    await cp(join(site, "docs"), site, { recursive: true });
    await rm(join(site, "docs"), { recursive: true });
  }
  await cp(join(site, "404/index.html"), join(site, "404.html"));
}

// Static Web Server needs the hashes of inline scripts in both prerendered trees.
const hashes = new Set<string>();
for (const output of ["site", "site-mounted"]) {
  for await (const page of new Glob("**/*.html").scan({
    cwd: join(docs, output),
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
const sections = new Set([
  "docs",
  ...(await readdir(join(docs, "site"), { withFileTypes: true }))
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name),
]);
config = config.replaceAll(
  "/{docs}",
  `/{${Array.from(sections).sort().join(",")}}`,
);
config = config.replace(
  'host = "docs.loco.build"',
  `host = ${JSON.stringify(new URL(url).hostname)}`,
);
await write(join(docs, ".sws.toml"), config);
