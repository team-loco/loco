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
		variant: "success",
		dot: "bg-success dark:bg-success",
	},
	deploying: {
		variant: "info",
		dot: "bg-info dark:bg-info",
	},
	stopped: {
		variant: "secondary",
		dot: "bg-text-quaternary dark:bg-text-quaternary",
	},
	failed: {
		variant: "error",
		dot: "bg-error dark:bg-error",
	},
	pending: {
		variant: "warning",
		dot: "bg-warning dark:bg-warning",
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
