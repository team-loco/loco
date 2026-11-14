import { cp, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";

import tailwind from "bun-plugin-tailwind";

import envPlugin from "./env-plugin";
import { collectImports, vendorEntries } from "./vendor-chunks";

const outdir = "dist";
const unknownVersion = "(devel)";
const linkedVersion = process.env["VERSION"] ?? "";
const version = linkedVersion === "" ? unknownVersion : linkedVersion;
const plugins = [tailwind, envPlugin];
const themeScript = "theme.js";
const spaFallback = "spa.html";

const injectedModules = ["react/jsx-runtime", "react/compiler-runtime"];

async function appSources(): Promise<string[]> {
	const graph = await Bun.build({ entrypoints: ["./index.html"], plugins, metafile: true, throw: true });
	const files = Object.keys(graph.metafile?.inputs ?? {}).filter(
		(file) => !file.includes("node_modules") && /\.tsx?$/.test(file),
	);
	return await Promise.all(files.map(async (file) => await Bun.file(file).text()));
}

/**
 * The 404 the static server answers with for missing files and unknown documentation paths. It is the app's
 * NotFound page rendered without script (the CSP forbids inline script) and with its CSS inlined, so it
 * loads nothing from /assets, which does not resolve on the documentation host.
 */
async function staticNotFound(): Promise<string> {
	const built = await Bun.build({ entrypoints: ["./src/not-found-static.css"], plugins, minify: true, throw: true });
	const [output] = built.outputs;
	if (output === undefined) throw new Error("the static 404 stylesheet has no output");
	// There is no script to set the .dark class, so dark tokens follow the browser. Unused themes are dropped.
	const lightAndDark = (await output.text())
		.replace(/(?:\.dark)?\[data-theme=(?!loco\])[^\]]+\]\{[^{}]*\}/g, "")
		.replace(/\.dark\{([^{}]*)\}/, "@media (prefers-color-scheme:dark){:root{$1}}");
	if (!lightAndDark.includes("@media (prefers-color-scheme:dark){:root{"))
		throw new Error("no dark theme in the static 404 stylesheet");
	const logo = Buffer.from(await Bun.file("public/logo.svg").arrayBuffer()).toString("base64");
	const scratch = await mkdtemp(join(tmpdir(), "loco-not-found-"));
	try {
		const render = await Bun.build({
			entrypoints: ["./src/not-found-static.tsx"],
			target: "bun",
			plugins,
			outdir: scratch,
			throw: true,
		});
		const [script] = render.outputs;
		if (script === undefined) throw new Error("the static 404 renderer has no output");
		const { renderNotFoundPage } = (await import(script.path)) as {
			renderNotFoundPage: (css: string, logoUrl: string) => string;
		};
		return renderNotFoundPage(lightAndDark, `data:image/svg+xml;base64,${logo}`);
	} finally {
		await rm(scratch, { recursive: true, force: true });
	}
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
await Bun.build({
	entrypoints: ["./src/theme-boot.ts"],
	outdir,
	naming: themeScript,
	format: "iife",
	minify: true,
	throw: true,
});
for (const output of result.outputs) {
	if (vendorOutputs.has(basename(output.path))) {
		continue;
	}
	const target = `${outdir}/${output.path.replace(/^\.\//, "")}`;
	if (output.path.endsWith(".html")) {
		const tagged = new HTMLRewriter().on("head", {
			element(head) {
				head.prepend(`<script src="/runtime-config.js"></script><script src="/${themeScript}"></script>`, {
					html: true,
				});
				head.append(`<meta name="loco-version" content="${Bun.escapeHTML(version)}">`, { html: true });
			},
		});
		await Bun.write(target, await tagged.transform(new Response(output)).text());
	} else {
		await Bun.write(target, output);
	}
}
// Every path but / is answered with this copy: signed-in pages and unknown paths stay out of search
// results, while the splash at / (index.html) stays indexable.
const unindexed = new HTMLRewriter().on("head", {
	element(head) {
		head.append('<meta name="robots" content="noindex">', { html: true });
	},
});
await Bun.write(
	`${outdir}/${spaFallback}`,
	await unindexed.transform(new Response(Bun.file(`${outdir}/index.html`))).text(),
);
await Bun.write(`${outdir}/404.html`, await staticNotFound());
await Bun.write(`${outdir}/version.json`, JSON.stringify({ version }));
