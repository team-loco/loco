import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dir, "..");
const files = [
	"theme.css",
	...readdirSync(import.meta.dir)
		.filter((f) => f.endsWith(".css"))
		.map((f) => `themes/${f}`),
];
const css = files.map((f) => readFileSync(join(root, f), "utf8")).join("\n");

const SURFACES = ["bg", "panel", "shell"];

// Collects custom properties declared in the block for a theme and mode, base theme first.
function tokens(theme: string, dark: boolean): Record<string, string> {
	const out: Record<string, string> = {};
	const selectors = theme === "loco" ? [':root,\\s*\\[data-theme="loco"\\]'] : [`\\[data-theme="${theme}"\\]`];
	if (dark) selectors.push(theme === "loco" ? "\\.dark" : `\\.dark\\[data-theme="${theme}"\\]`);
	for (const sel of selectors) {
		for (const m of css.matchAll(new RegExp(`(?:^|\\n)\\s*${sel}\\s*\\{([^}]*)\\}`, "g"))) {
			for (const d of m[1]!.matchAll(/(--[\w-]+):\s*([^;]+);/g)) out[d[1]!] = d[2]!.trim();
		}
	}
	return out;
}

function luminance(hex: string): number {
	const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
	const [r, g, b] = c.map((x) => (x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4)) as [number, number, number];
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a: string, b: string): number {
	const [hi, lo] = [luminance(a), luminance(b)].toSorted((x, y) => y - x) as [number, number];
	return (hi + 0.05) / (lo + 0.05);
}

const themes = ["loco", "paper", "neobrutal", "catppuccin", "solarized", "rose-pine"];

for (const theme of themes) {
	for (const dark of [false, true]) {
		test(`the debug level colour reads at 3:1 on the page, panel and shell: ${theme} ${dark ? "dark" : "light"}`, () => {
			const t = tokens(theme, dark);
			const base = tokens("loco", dark);
			const get = (name: string) => t[name] ?? base[name]!;
			const ref = /^var\((--[\w-]+)\)$/.exec(get("--lv-debug"));
			const debug = ref ? get(ref[1]!) : get("--lv-debug");
			for (const surface of SURFACES) {
				expect(ratio(debug, get(`--${surface}`))).toBeGreaterThanOrEqual(3);
			}
		});

		test(`the breadcrumb separator colour reads at 3:1 on the page, panel and shell: ${theme} ${dark ? "dark" : "light"}`, () => {
			const t = tokens(theme, dark);
			const base = tokens("loco", dark);
			const get = (name: string) => t[name] ?? base[name]!;
			for (const surface of SURFACES) {
				expect(ratio(get("--fg3"), get(`--${surface}`))).toBeGreaterThanOrEqual(3);
			}
		});
	}
}

for (const dark of [false, true]) {
	test(`catppuccin text, button and accent fill pairs read at 4.5:1: ${dark ? "dark" : "light"}`, () => {
		const t = { ...tokens("loco", dark), ...tokens("catppuccin", dark) };
		for (const surface of [...SURFACES, "accent-soft"]) {
			expect(ratio(t["--fg"]!, t[`--${surface}`]!)).toBeGreaterThanOrEqual(4.5);
		}
		for (const surface of SURFACES) {
			expect(ratio(t["--fg2"]!, t[`--${surface}`]!)).toBeGreaterThanOrEqual(4.5);
			expect(ratio(t["--brand"]!, t[`--${surface}`]!)).toBeGreaterThanOrEqual(4.5);
		}
		expect(ratio(t["--btn-fg"]!, t["--btn"]!)).toBeGreaterThanOrEqual(4.5);
	});
}
