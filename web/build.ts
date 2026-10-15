import { cp, rm } from "node:fs/promises";
import { basename } from "node:path";

import tailwind from "bun-plugin-tailwind";

import envPlugin from "./env-plugin";
import { collectImports, vendorEntries } from "./vendor-chunks";

const outdir = "dist";
const unknownVersion = "(devel)";
const linkedVersion = process.env["VERSION"] ?? "";
const version = linkedVersion === "" ? unknownVersion : linkedVersion;
const plugins = [tailwind, envPlugin];

const injectedModules = ["react/jsx-runtime", "react/compiler-runtime"];

async function appSources(): Promise<string[]> {
	const graph = await Bun.build({ entrypoints: ["./index.html"], plugins, metafile: true, throw: true });
	const files = Object.keys(graph.metafile?.inputs ?? {}).filter((file) => !file.includes("node_modules") && /\.tsx?$/.test(file));
	return await Promise.all(files.map(async (file) => await Bun.file(file).text()));
}

const entries = vendorEntries(collectImports(await appSources(), injectedModules));
const vendorOutputs = new Set(Object.keys(entries).map((entry) => basename(entry).replace(/\.ts$/, ".js")));
const result = await Bun.build({
	entrypoints: ["./index.html", ...Object.keys(entries)],
	files: entries,
	plugins,
	define: { "process.env.NODE_ENV": JSON.stringify("production") },
	minify: true,
	splitting: true,
	reactCompiler: true,
	publicPath: "/",
	naming: { chunk: "assets/[name]-[hash].[ext]", asset: "assets/[name]-[hash].[ext]" },
	throw: true,
});

await rm(outdir, { recursive: true, force: true });
await cp("public", outdir, { recursive: true });
for (const output of result.outputs) {
	if (vendorOutputs.has(basename(output.path))) {
		continue;
	}
	const target = `${outdir}/${output.path.replace(/^\.\//, "")}`;
	if (output.path.endsWith(".html")) {
		const tagged = new HTMLRewriter().on("head", {
			element(head) {
				head.prepend('<script src="/runtime-config.js"></script>', { html: true });
				head.append(`<meta name="loco-version" content="${Bun.escapeHTML(version)}">`, { html: true });
			},
		});
		await Bun.write(target, await tagged.transform(new Response(output)).text());
	} else {
		await Bun.write(target, output);
	}
}
await Bun.write(`${outdir}/version.json`, JSON.stringify({ version }));
