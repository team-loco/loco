import { NAME_RE, type Draft } from "./drafts";

export interface DraftErrors {
	region: string | null;
	sub: string | null;
	port: string | null;
	cpuTarget: string | null;
	vars: string | null;
	health: string | null;
}

function positiveInt(v: string): boolean {
	const n = Number(v);
	return Number.isInteger(n) && n >= 1;
}

export function validateDraft(
	draft: Draft,
	region: string,
	platformDomain: string,
	takenSubs: Set<string>,
	availability: "available" | "taken" | "unknown" | "checking",
): DraftErrors {
	const sub = draft.sub.trim();
	const port = Number(draft.port);
	const target = Number(draft.cpuTarget);
	const keys = draft.vars.map((v) => v.key.trim()).filter((k) => k !== "");
	const badKey = keys.find((k) => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(k));
	const dupKey = keys.find((k, i) => keys.indexOf(k) !== i);
	const full = `${sub}.${platformDomain}`;

	return {
		region: region === "" ? "Choose a region" : null,
		sub:
			sub === ""
				? "Choose a subdomain"
				: !NAME_RE.test(sub)
					? "Lowercase letters, numbers and hyphens"
					: takenSubs.has(sub) || availability === "taken"
						? `${full} is taken`
						: null,
		port: !Number.isInteger(port) || port < 1024 || port > 65535 ? "Port must be 1024–65535" : null,
		cpuTarget:
			draft.min !== draft.max && (!Number.isInteger(target) || target < 1 || target > 100)
				? "CPU target must be 1–100%"
				: null,
		vars:
			badKey !== undefined
				? `${badKey} is not a valid variable name`
				: dupKey !== undefined
					? `${dupKey} is set twice`
					: null,
		health: !draft.hcPath.startsWith("/")
			? "Health check path must start with /"
			: !positiveInt(draft.hcInterval) || !positiveInt(draft.hcTimeout) || !positiveInt(draft.hcFail)
				? "Interval, timeout and fail after must be whole numbers of at least 1"
				: null,
	};
}

export function firstError(e: DraftErrors): string | null {
	return e.region ?? e.sub ?? e.port ?? e.cpuTarget ?? e.vars ?? e.health;
}
