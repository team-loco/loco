import { Badge } from "@/components/ui/badge";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import { getResourceStatusTooltip } from "@/lib/deployment-utils";

interface StatusBadgeProps {
	status: string;
	showTooltip?: boolean;
}

const statusConfig: Record<string, { variant: string; dot: string }> = {
	running: {
		variant: "neo-green",
		dot: "bg-green-900 dark:bg-green-100",
	},
	deploying: {
		variant: "neo-blue",
		dot: "bg-blue-900 dark:bg-blue-100",
	},
	stopped: {
		variant: "neo-gray",
		dot: "bg-gray-900 dark:bg-gray-100",
	},
	failed: {
		variant: "neo-red",
		dot: "bg-red-900 dark:bg-red-100",
	},
	pending: {
		variant: "neo-orange",
		dot: "bg-orange-900 dark:bg-orange-100",
	},
};

export function StatusBadge({ status, showTooltip = true }: StatusBadgeProps) {
	const normalizedStatus = status.toLowerCase();
	const config = statusConfig[normalizedStatus] || statusConfig.pending;
	const isPulsing =
		normalizedStatus === "running" || normalizedStatus === "deploying";

	const badge = (
		<Badge
			variant={config.variant as any}
			className="flex items-center gap-2"
		>
			<span
				className={`w-2 h-2 rounded-full shrink-0 inline-block ${config.dot} ${
					isPulsing ? "animate-pulse" : ""
				}`}
			></span>
			{status.charAt(0).toUpperCase() + status.slice(1)}
		</Badge>
	);

	if (!showTooltip) {
		return badge;
	}

	return (
		<Tooltip>
			<TooltipTrigger asChild>{badge}</TooltipTrigger>
			<TooltipContent>{getResourceStatusTooltip(status)}</TooltipContent>
		</Tooltip>
	);
}
