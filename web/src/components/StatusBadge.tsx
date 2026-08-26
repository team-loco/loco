import { Badge, badgeVariants } from "@/components/design/Badge";
import { type VariantProps } from "class-variance-authority";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "@/components/design/Tooltip";
import { getResourceStatusTooltip } from "@/lib/deployment-utils";
import type { ResourceStatusLabel } from "@/lib/app-status";

type BadgeVariant = VariantProps<typeof badgeVariants>["variant"];

interface StatusBadgeProps {
	status: ResourceStatusLabel;
	showTooltip?: boolean;
}

interface StatusConfig {
	variant: BadgeVariant;
	dot: string;
}

const statusConfig: Record<ResourceStatusLabel, StatusConfig> = {
	running: {
		variant: "success",
		dot: "bg-success dark:bg-success",
	},
	deploying: {
		variant: "info",
		dot: "bg-info dark:bg-info",
	},
	degraded: {
		variant: "warning",
		dot: "bg-warning dark:bg-warning",
	},
	unavailable: {
		variant: "error",
		dot: "bg-error dark:bg-error",
	},
	suspended: {
		variant: "secondary",
		dot: "bg-text-quaternary dark:bg-text-quaternary",
	},
	pending: {
		variant: "warning",
		dot: "bg-warning dark:bg-warning",
	},
};

export function StatusBadge({ status, showTooltip = true }: StatusBadgeProps) {
	const config = statusConfig[status];
	const isPulsing = status === "running" || status === "deploying";

	const badge = (
		<Badge
			variant={config.variant}
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
			<TooltipTrigger render={badge} />
			<TooltipContent>{getResourceStatusTooltip(status)}</TooltipContent>
		</Tooltip>
	);
}
