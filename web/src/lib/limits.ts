import { formatCount } from "@/lib/format";

/** How many of something exist against the count limit that caps them. */
export type LimitUsage = {
	count: number;
	limit: number;
	/** The plural of what is counted, such as "workspaces". */
	noun: string;
	/** What holds them, completing "in this ...": "organization", "workspace". */
	scope: string;
};

export type LimitTone = "warn" | "bad";

/** Returns the usage once the limit is known; a zero or missing limit means it has not loaded. */
export function limitUsage(count: number, limit: number | undefined, noun: string, scope: string): LimitUsage | null {
	if (limit === undefined || limit <= 0) return null;
	return { count, limit, noun, scope };
}

export function usageLabel(usage: LimitUsage): string {
	return `${formatCount(usage.count)} / ${formatCount(usage.limit)}`;
}

/** Usage only matters from 90% of the limit: amber near it, red at it. */
export function limitTone(usage: LimitUsage | null): LimitTone | null {
	if (usage === null) return null;
	if (usage.count >= usage.limit) return "bad";
	return usage.count * 10 >= usage.limit * 9 ? "warn" : null;
}

/** Why a create is blocked at the limit, or null below it. */
export function limitReason(usage: LimitUsage | null): string | null {
	if (usage === null || usage.count < usage.limit) return null;
	return `You've reached the limit of ${formatCount(usage.limit)} ${usage.noun} in this ${usage.scope}. Delete one, or ask a Loco admin to raise it.`;
}
