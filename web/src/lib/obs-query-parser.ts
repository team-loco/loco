export interface ParsedQuery {
	search: string;
	levels: string[];
	labels: Record<string, string>;
}

const LEVEL_ALIASES: Record<string, string> = {
	debug: "DEBUG",
	info: "INFO",
	warn: "WARN",
	warning: "WARN",
	error: "ERROR",
	err: "ERROR",
	fatal: "FATAL",
	crit: "FATAL",
	critical: "FATAL",
};

const LABEL_KEYS: Record<string, string> = {
	pod: "k8s.pod.name",
	namespace: "k8s.namespace.name",
	resource: "resource_id",
	app: "app",
};

export function parseObsQuery(query: string): ParsedQuery {
	const levels: string[] = [];
	const labels: Record<string, string> = {};
	const searchParts: string[] = [];

	let remaining = query.trim();

	// Extract quoted strings
	remaining = remaining.replace(/"([^"]+)"/g, (_, phrase: string) => {
		searchParts.push(phrase.trim());
		return "";
	});

	// Extract key:value tokens
	remaining = remaining.replace(
		/(\w+):(\S+)/g,
		(_, key: string, value: string) => {
			const lk = key.toLowerCase();
			if (lk === "level" || lk === "severity") {
				const lvl = LEVEL_ALIASES[value.toLowerCase()];
				if (lvl && !levels.includes(lvl)) levels.push(lvl);
			} else {
				const labelKey = LABEL_KEYS[lk];
				if (labelKey) labels[labelKey] = value;
			}
			return "";
		},
	);

	// Remaining bare words go to search
	const bare = remaining.trim();
	if (bare) searchParts.push(bare);

	return {
		search: searchParts.join(" ").trim(),
		levels,
		labels,
	};
}

export function formatObsQuery(parsed: ParsedQuery): string {
	const parts: string[] = [];
	for (const lvl of parsed.levels) {
		parts.push(`level:${lvl.toLowerCase()}`);
	}
	for (const [k, v] of Object.entries(parsed.labels)) {
		const shortKey = Object.entries(LABEL_KEYS).find(([, full]) => full === k)?.[0] ?? k;
		parts.push(`${shortKey}:${v}`);
	}
	if (parsed.search) {
		parts.push(parsed.search.includes(" ") ? `"${parsed.search}"` : parsed.search);
	}
	return parts.join(" ");
}
