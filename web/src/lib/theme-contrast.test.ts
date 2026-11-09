import { describe, expect, test } from "bun:test";

import { colorThemes, defaultColorTheme, type ColorTheme } from "./theme";

const minimumContrast = 4.5;
const textTokens = ["--fg", "--fg2", "--fg3"];
const surfaceTokens = ["--bg", "--panel", "--panel2", "--raise", "--shell"];
const pairs = [
	...textTokens.flatMap((text) => surfaceTokens.map((surface) => [text, surface] as const)),
	["--btn-fg", "--btn"] as const,
	["--brand", "--bg"] as const,
	["--brand", "--panel"] as const,
];

const css = await Bun.file(new URL("../index.css", import.meta.url)).text();
const blocks = new Map<string, Map<string, string>>();
for (const [, selector = "", body = ""] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
	const declarations = new Map<string, string>();
	for (const [, name = "", value = ""] of body.matchAll(/(--[\w-]+):\s*([^;]+);/g)) {
		declarations.set(name, value.trim());
	}
	blocks.set(selector.trim().replace(/\s+/g, " "), declarations);
}

function block(selector: string): Map<string, string> {
	const found = blocks.get(selector);
	if (found === undefined) throw new Error(`index.css has no ${selector} block`);
	return found;
}

function tokens(color: ColorTheme, dark: boolean): Map<string, string> {
	const layers = [block(`:root, [data-theme="${defaultColorTheme}"]`)];
	if (dark) layers.push(block(".dark"));
	if (color !== defaultColorTheme) {
		layers.push(block(`[data-theme="${color}"]`));
		if (dark) layers.push(block(`.dark[data-theme="${color}"]`));
	}
	return new Map(layers.flatMap((layer) => [...layer]));
}

function luminance(hex: string): number {
	const match = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
	if (match === null) throw new Error(`${hex} is not a #rrggbb colour`);
	const [r = 0, g = 0, b = 0] = match.slice(1).map((part) => {
		const channel = Number.parseInt(part, 16) / 255;
		return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
	});
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
	const [first, second] = [luminance(a), luminance(b)];
	return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

describe("colour theme contrast", () => {
	for (const { id } of colorThemes) {
		for (const dark of [false, true]) {
			test(`${id} ${dark ? "dark" : "light"} keeps text at ${minimumContrast}:1`, () => {
				const values = tokens(id, dark);
				const failing = pairs
					.map(([text, surface]) => {
						const ratio = contrast(values.get(text) ?? "", values.get(surface) ?? "");
						return { pair: `${text} on ${surface}`, ratio: Math.round(ratio * 100) / 100 };
					})
					.filter(({ ratio }) => ratio < minimumContrast);
				expect(failing).toEqual([]);
			});
		}
	}
});
