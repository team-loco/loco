import type { ReactElement } from "react";

import { Badge } from "@/components/design/Badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { type LimitTone, type LimitUsage, limitTone, usageLabel } from "@/lib/limits";
import { cn } from "@/lib/utils";

const LIMIT_TONE_TEXT: Record<LimitTone, string> = { warn: "text-warn-fg", bad: "text-bad-fg" };
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

/** A title count pill: the plain count below 90% of the limit, "used / max" in amber or red from there. */
export function LimitCount({ count, usage }: { count: number; usage: LimitUsage | null }) {
	const tone = limitTone(usage);
	if (usage === null || tone === null) return <Badge tone="count">{count}</Badge>;
	return (
		<Badge tone="count" className={TONE_PILL[tone]}>
			{usageLabel(usage)}
		</Badge>
	);
}

/**
 * A create action with its limit usage beside it once that nears the limit. With a reason, the
 * action renders as given (the caller disables it) and the reason shows as its tooltip.
 */
export function LimitedAction({
	usage,
	reason,
	children,
}: {
	usage: LimitUsage | null;
	reason: string | null;
	children: ReactElement;
}) {
	return (
		<span className="inline-flex items-center gap-2.5">
			<LimitUsageText usage={usage} />
			{reason === null ? (
				children
			) : (
				<Tooltip>
					<TooltipTrigger render={<span className="inline-flex" />}>{children}</TooltipTrigger>
					<TooltipContent className="max-w-[280px]">{reason}</TooltipContent>
				</Tooltip>
			)}
		</span>
	);
}
