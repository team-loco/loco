import type { ReactElement } from "react";

import { Badge } from "@/components/design/Badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { type LimitTone, type LimitUsage, limitTone, usageLabel } from "@/lib/limits";
import { cn } from "@/lib/utils";

export const LIMIT_TONE_TEXT: Record<LimitTone, string> = { warn: "text-warn-fg", bad: "text-bad-fg" };
const TONE_PILL: Record<LimitTone, string> = { warn: "bg-warn-bg text-warn-fg", bad: "bg-bad-bg text-bad-fg" };

/** "used / max" in amber or red from 90% of the limit; nothing below it. */
export function LimitUsageText({ usage, className }: { usage: LimitUsage | null; className?: string }) {
	const tone = limitTone(usage);
	if (usage === null || tone === null) return null;
	return (
		<span className={cn("font-mono text-[12px] tabular-nums", LIMIT_TONE_TEXT[tone], className)}>
			{usageLabel(usage)}
		</span>
	);
}

/** Title usage appears from 90% of the limit. */
export function LimitCount({ usage }: { usage: LimitUsage | null }) {
	const tone = limitTone(usage);
	if (usage === null || tone === null) return null;
	return (
		<Badge tone="count" className={TONE_PILL[tone]}>
			{usageLabel(usage)}
		</Badge>
	);
}

/** A disabled create action explains its limit in a tooltip. */
export function LimitedAction({ reason, children }: { reason: string | null; children: ReactElement }) {
	if (reason === null) return children;
	return (
		<Tooltip>
			<TooltipTrigger render={<span className="inline-flex" />}>{children}</TooltipTrigger>
			<TooltipContent className="max-w-[280px]">{reason}</TooltipContent>
		</Tooltip>
	);
}
