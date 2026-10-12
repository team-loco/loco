const vendorGroups: Record<string, RegExp> = {
	react: /^(react|react-dom)(\/|$)/,
	"base-ui": /^@base-ui\//,
	tanstack: /^@tanstack\//,
	recharts: /^recharts(\/|$)/,
	"sugar-high": /^sugar-high(\/|$)/,
	generated: /^@gen\//,
	vendor: /^(?!@\/|@gen\/|loco:)/,
};
const importPattern = /(?:import|export)\s+(?!type\s)([^"';]+?)\s+from\s+["']([^"'.][^"']*)["']/g;

export function collectImports(sources: string[], injected: string[]): Map<string, Set<string>> {
	const imported = new Map<string, Set<string>>(injected.map((spec) => [spec, new Set(["*"])]));
	for (const source of sources) {
		for (const [, clause = "", spec = ""] of source.matchAll(importPattern)) {
			const names = imported.get(spec) ?? new Set<string>();
			const braces = /\{([\s\S]*)\}/.exec(clause)?.[1] ?? "";
			for (const specifier of braces.split(",")) {
				const name = specifier.trim().split(/\s+as\s+/)[0] ?? "";
				if (name !== "" && !name.startsWith("type ")) {
					names.add(name);
				}
			}
			const head = clause.replace(/\{[\s\S]*\}/, "").replace(/,\s*$/, "").trim();
			if (head === "*" || head.startsWith("* as")) {
				names.add("*");
			} else if (head !== "") {
				names.add("default");
			}
			imported.set(spec, names);
		}
	}
	return imported;
}

export function vendorEntries(imported: Map<string, Set<string>>): Record<string, string> {
	const entries: Record<string, string> = {};
	for (const group of Object.keys(vendorGroups)) {
		const lines: string[] = [];
		let alias = 0;
		for (const [spec, names] of imported) {
			const owner = Object.entries(vendorGroups).find(([, pattern]) => pattern.test(spec))?.[0];
			if (owner !== group) {
				continue;
			}
			if (names.has("*")) {
				lines.push(`export * as v${String(alias++)} from "${spec}";`);
			}
			const named = [...names].filter((name) => name !== "*").map((name) => `${name} as v${String(alias++)}`);
			if (named.length > 0) {
				lines.push(`export { ${named.join(", ")} } from "${spec}";`);
			}
		}
		entries[`./vendor-${group}.ts`] = lines.join("\n");
	}
	return entries;
}
