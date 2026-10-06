import { isEnvKey } from "@/lib/dotenv";

export type EnvPair = [string, string];

export function mergeEnv(base: EnvPair[], incoming: EnvPair[]): EnvPair[] {
	const map = new Map<string, string>(base);
	for (const [k, v] of incoming) map.set(k, v);
	return [...map.entries()].filter(([k]) => k !== "");
}

export function validateEnv(pairs: EnvPair[]): string | undefined {
	if (pairs.length === 0) return "Add at least one variable.";
	const seen = new Set<string>();
	for (const [k] of pairs) {
		if (k === "") return "Every variable needs a name.";
		if (!isEnvKey(k)) return `${k} is not a valid name. Use letters, digits and underscores, not starting with a digit.`;
		if (seen.has(k)) return `${k} is defined more than once.`;
		seen.add(k);
	}
	return undefined;
}
