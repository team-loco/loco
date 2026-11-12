import { formatCount } from "@/lib/format";

/** How many of something exist against the count limit that caps them. */
export type LimitUsage = {
	count: number;
	limit: number;
	/** The plural of what is counted, such as "workspaces". */
	noun: string;
	/** Who holds them, as the start of a sentence: "This workspace has", "You created". */
	holder: string;
};

/** Returns the usage once the limit is known; a zero or missing limit means it has not loaded. */
export function limitUsage(count: number, limit: number | undefined, noun: string, holder: string): LimitUsage | null {
	if (limit === undefined || limit <= 0) return null;
	return { count, limit, noun, holder };
}

export function usageLabel(usage: LimitUsage): string {
	return `${formatCount(usage.count)} of ${formatCount(usage.limit)} ${usage.noun}`;
}

/** Why a create is blocked at the limit, or null below it. */
export function limitReason(usage: LimitUsage | null): string | null {
	if (usage === null || usage.count < usage.limit) return null;
	return `${usage.holder} ${usageLabel(usage)}. Delete one or ask a Loco administrator to raise the limit.`;
}
