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

const statusConfig: Record<string, { className: string; dot: string }> = {
	running: {
		className:
			"border-teal-600 text-teal-700 dark:border-teal-400 dark:text-teal-300",
		dot: "bg-teal-600 dark:bg-teal-400",
	},
	deploying: {
		className:
			"border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-300",
		dot: "bg-blue-600 dark:bg-blue-400",
	},
	stopped: {
		className:
			"border-gray-500 text-gray-700 dark:border-gray-500 dark:text-gray-300",
		dot: "bg-gray-500 dark:bg-gray-400",
	},
	failed: {
		className:
			"border-red-600 text-red-700 dark:border-red-400 dark:text-red-300",
		dot: "bg-red-600 dark:bg-red-400",
	},
	pending: {
		className:
			"border-orange-600 text-orange-700 dark:border-orange-400 dark:text-orange-300",
		dot: "bg-orange-600 dark:bg-orange-400",
	},
};

export function StatusBadge({ status, showTooltip = true }: StatusBadgeProps) {
	const normalizedStatus = status.toLowerCase();
	const config = statusConfig[normalizedStatus] || statusConfig.pending;
	const isPulsing =
		normalizedStatus === "running" || normalizedStatus === "deploying";

	const badge = (
		<Badge
			variant="outline"
			className={`${config.className} flex items-center gap-2`}
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
