import type { CountLimits } from "@gen/loco/config/v1/config_pb";

import { formatCount } from "@/lib/format";

/** The numeric fields of CountLimits. */
export type LimitKey = { [K in keyof CountLimits]-?: CountLimits[K] extends number ? K : never }[keyof CountLimits];

/** GetOrgLimits lists a raised limit by its proto field name. */
export const protoField = (key: LimitKey) => key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`);

/** How many of something exist against the count limit that caps them. */
export type LimitUsage = {
	count: number;
	limit: number;
	/** The plural of what is counted, such as "workspaces". */
	noun: string;
	/** The owner or containing entity: "organization", "workspace", "personal", "account". */
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

const SCOPE_LABEL: Record<string, string> = {
	organization: "Org",
	workspace: "Workspace",
	personal: "Personal",
	account: "Account",
};

export function limitPillLabel(usage: LimitUsage): string {
	const scope = SCOPE_LABEL[usage.scope];
	if (usage.count >= usage.limit) return scope === undefined ? "Limit reached" : `${scope} limit reached`;
	return `${formatCount(usage.count)} of ${formatCount(usage.limit)} used`;
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
	return `You've reached the limit of ${formatCount(usage.limit)} ${usage.noun}${["", "personal", "account"].includes(usage.scope) ? "" : ` in this ${usage.scope}`}. Delete one, or ask a Loco admin to raise it.`;
}
