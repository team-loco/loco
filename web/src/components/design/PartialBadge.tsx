import { FileCogIcon } from "lucide-react";

import { Badge } from "./Badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "./Tooltip";

export function PartialBadge({ partial }: { partial: string }) {
	const label = `Managed by loco.yaml (${partial})`;
	return (
		<Tooltip>
			<TooltipTrigger
				render={<Badge tone="info" size="sm" tabIndex={0} aria-label={label} className="max-w-full min-w-0 shrink text-sm" />}
			>
				<FileCogIcon />
				<span className="min-w-0 truncate">{label}</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-[min(90vw,32rem)] break-all">{label}</TooltipContent>
		</Tooltip>
	);
}
