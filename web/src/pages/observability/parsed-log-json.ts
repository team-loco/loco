// Preserve numeric tokens in parsed previews, including integers beyond JavaScript's safe range.
declare global {
	interface JSON {
		rawJSON(text: string): { readonly rawJSON: string };
	}
}

function parsedContainer(text: string): unknown {
	const trimmed = text.trim();
	if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null;
	try {
		return JSON.parse(trimmed, (_key: string, value: unknown, context?: { source?: string }) => {
			if (typeof value === "number" && context?.source !== undefined) return JSON.rawJSON(context.source);
			return value;
		}) as unknown;
	} catch {
		return null;
	}
}

/** An optional display projection. The original log and its filter values stay untouched. */
export function parsedLogJson(original: string): string | null {
	const log = JSON.parse(original) as Record<string, unknown>;
	let found = false;
	for (const field of ["resource_attributes", "log_attributes"]) {
		const attributes = log[field] as Record<string, string>;
		const entries: [string, unknown][] = [];
		for (const [key, value] of Object.entries(attributes)) {
			const parsed = parsedContainer(value);
			if (parsed !== null) found = true;
			entries.push([key, parsed ?? value]);
		}
		log[field] = Object.fromEntries(entries);
	}
	return found ? JSON.stringify(log, null, 2) : null;
}
