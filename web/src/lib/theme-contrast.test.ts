import { describe, expect, test } from "bun:test";

import { colorThemes, defaultColorTheme, type ColorTheme } from "./theme";

const minimumContrast = 4.5;
const minimumFocusContrast = 3;
const focusPairs = [
	["--ring", "--bg"],
	["--ring", "--panel"],
	["--sidebar-ring", "--shell"],
] as const;
const maximumHueShift = 20;
const statusPairs = [
	["--ok", "--color-ok-fg"],
	["--info", "--color-info-fg"],
	["--warn", "--color-warn-fg"],
	["--err", "--color-bad-fg"],
] as const;
const textTokens = ["--fg", "--fg2", "--fg3"];
const surfaceTokens = ["--bg", "--panel", "--panel2", "--raise", "--shell"];
const pairs = [
	...textTokens.flatMap((text) => surfaceTokens.map((surface) => [text, surface] as const)),
	["--btn-fg", "--btn"] as const,
	["--brand", "--bg"] as const,
	["--brand", "--panel"] as const,
	["--brand", "--shell"] as const,
	["--fg", "--shell-active"] as const,
];

const sheets = [
	"../theme.css",
	...colorThemes.filter(({ id }) => id !== defaultColorTheme).map(({ id }) => `../themes/${id}.css`),
];
const css = (
	await Promise.all(sheets.map(async (sheet) => await Bun.file(new URL(sheet, import.meta.url)).text()))
).join("\n");
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
	if (found === undefined) throw new Error(`theme.css has no ${selector} block`);
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

type Rgb = [number, number, number];

function parseColor(color: string): Rgb {
	const oklch = /^oklch\(([\d.]+) ([\d.]+) ([\d.]+)\)$/.exec(color);
	if (oklch !== null) {
		const [lightness = 0, chroma = 0, hue = 0] = oklch.slice(1).map(Number);
		const radians = (hue * Math.PI) / 180;
		return fromOklab([lightness, chroma * Math.cos(radians), chroma * Math.sin(radians)]);
	}
	const match = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(color);
	if (match === null) throw new Error(`${color} is not a #rrggbb or oklch() colour`);
	const [r = 0, g = 0, b = 0] = match.slice(1).map((part) => Number.parseInt(part, 16) / 255);
	return [r, g, b];
}

const toLinear = (channel: number) => (channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4);
const fromLinear = (channel: number) => (channel <= 0.0031308 ? channel * 12.92 : 1.055 * channel ** (1 / 2.4) - 0.055);

function luminance([r, g, b]: Rgb): number {
	return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b);
}

function contrast(a: Rgb, b: Rgb): number {
	const [first, second] = [luminance(a), luminance(b)];
	return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

function toOklab([r, g, b]: Rgb): Rgb {
	const [lr, lg, lb] = [toLinear(r), toLinear(g), toLinear(b)];
	const l = Math.cbrt(0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb);
	const m = Math.cbrt(0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb);
	const s = Math.cbrt(0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb);
	return [
		0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
		1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
		0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
	];
}

function fromOklab([lightness, a, b]: Rgb): Rgb {
	const l = (lightness + 0.3963377774 * a + 0.2158037573 * b) ** 3;
	const m = (lightness - 0.1055613458 * a - 0.0638541728 * b) ** 3;
	const s = (lightness - 0.0894841775 * a - 1.291485548 * b) ** 3;
	const linear = [
		4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
		-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
		-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
	];
	const [r = 0, g = 0, b2 = 0] = linear.map((channel) => Math.min(1, Math.max(0, fromLinear(channel))));
	return [r, g, b2];
}

const hueOf = ([, a, b]: Rgb) => (Math.atan2(b, a) * 180) / Math.PI;

function hueDistance(a: number, b: number): number {
	const diff = Math.abs(a - b) % 360;
	return diff > 180 ? 360 - diff : diff;
}

// Resolves `color-mix(in <space> <first> <pct>%, <second>)` the way a browser does for opaque
// sRGB inputs; polar spaces interpolate hue along the shorter arc.
function mix(declaration: string, values: Map<string, string>): Rgb {
	const match = /^color-mix\(in (\w+), var\((--[\w-]+)\) (\d+)%, var\((--[\w-]+)\)\)$/.exec(declaration);
	if (match === null) throw new Error(`cannot evaluate ${declaration}`);
	const [, space = "", first = "", percent = "", second = ""] = match;
	const weight = Number(percent) / 100;
	const [one, two] = [first, second].map((name) => toOklab(parseColor(values.get(name) ?? "")));
	if (one === undefined || two === undefined) throw new Error(`cannot evaluate ${declaration}`);
	if (space === "oklab") {
		return fromOklab([0, 1, 2].map((i) => weight * (one[i] ?? 0) + (1 - weight) * (two[i] ?? 0)) as Rgb);
	}
	if (space !== "oklch") throw new Error(`unsupported colour space ${space}`);
	const [chroma1, chroma2] = [Math.hypot(one[1], one[2]), Math.hypot(two[1], two[2])];
	let [hue1, hue2] = [hueOf(one), hueOf(two)];
	if (hue2 - hue1 > 180) hue1 += 360;
	if (hue1 - hue2 > 180) hue2 += 360;
	const hue = ((weight * hue1 + (1 - weight) * hue2) * Math.PI) / 180;
	const chroma = weight * chroma1 + (1 - weight) * chroma2;
	return fromOklab([weight * one[0] + (1 - weight) * two[0], chroma * Math.cos(hue), chroma * Math.sin(hue)]);
}

function themeDeclaration(name: string): string {
	const found = /(--[\w-]+):\s*([^;]+);/g;
	for (const [, declared, value = ""] of css.matchAll(found)) {
		if (declared === name) return value.trim();
	}
	throw new Error(`theme.css does not declare ${name}`);
}

function resolve(name: string, values: Map<string, string>): Rgb {
	const value = values.get(name);
	const reference = /^var\((--[\w-]+)\)$/.exec(value ?? "");
	return reference?.[1] === undefined ? parseColor(value ?? "") : resolve(reference[1], values);
}

describe("colour theme contrast", () => {
	for (const { id } of colorThemes) {
		for (const dark of [false, true]) {
			test(`${id} ${dark ? "dark" : "light"} keeps text at ${minimumContrast}:1`, () => {
				const values = tokens(id, dark);
				const failing = pairs
					.map(([text, surface]) => {
						const ratio = contrast(resolve(text, values), resolve(surface, values));
						return { pair: `${text} on ${surface}`, ratio: Math.round(ratio * 100) / 100 };
					})
					.filter(({ ratio }) => ratio < minimumContrast);
				expect(failing).toEqual([]);
			});

			test(`${id} ${dark ? "dark" : "light"} keeps the focus ring at ${minimumFocusContrast}:1`, () => {
				const values = tokens(id, dark);
				const failing = focusPairs
					.map(([ring, surface]) => {
						const ratio = contrast(resolve(ring, values), resolve(surface, values));
						return { pair: `${ring} on ${surface}`, ratio: Math.round(ratio * 100) / 100 };
					})
					.filter(({ ratio }) => ratio < minimumFocusContrast);
				expect(failing).toEqual([]);
			});

			test(`${id} ${dark ? "dark" : "light"} keeps status text on the hue of its status colour`, () => {
				const values = tokens(id, dark);
				const shifted = statusPairs
					.map(([source, text]) => {
						const mixed = toOklab(mix(themeDeclaration(text), values));
						const shift = hueDistance(hueOf(mixed), hueOf(toOklab(resolve(source, values))));
						return { token: text, shift: Math.round(shift) };
					})
					.filter(({ shift }) => shift > maximumHueShift);
				expect(shifted).toEqual([]);
			});
		}
	}
});
