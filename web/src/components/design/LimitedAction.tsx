import type { ReactElement } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { type LimitUsage, usageLabel } from "@/lib/limits";

/**
 * A create action with the usage of the limit that caps it beside it. With a reason, the action
 * renders as given (the caller disables it) and the reason shows as its tooltip.
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
			{usage !== null && <span className="text-[12.5px] text-fg3 tabular-nums">{usageLabel(usage)}</span>}
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
