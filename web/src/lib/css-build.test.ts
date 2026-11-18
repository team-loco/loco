import { expect, test } from "bun:test";

import tailwind from "bun-plugin-tailwind";

const compact = (css: string) => css.replace(/\s+/g, "").replace(/\b0\./g, ".");

const built = await Bun.build({
	entrypoints: [new URL("../index.css", import.meta.url).pathname],
	plugins: [tailwind],
	minify: true,
	throw: true,
});
const output = compact((await Promise.all(built.outputs.map(async (file) => await file.text()))).join(""));

test("every color-mix() custom property survives the build", async () => {
	const source = await Bun.file(new URL("../theme.css", import.meta.url)).text();
	const declarations = [...source.matchAll(/(--(?!color-)[\w-]+):\s*(color-mix\([^;]+);/g)].map(
		([, name = "", value = ""]) => compact(`${name}:${value}`),
	);
	expect(declarations.length).toBeGreaterThan(0);
	expect(declarations.filter((declaration) => !output.includes(declaration))).toEqual([]);
});

test("no keyframes build to an empty rule", () => {
	expect(output.match(/@keyframes[\w-]+\{\}/g) ?? []).toEqual([]);
});
