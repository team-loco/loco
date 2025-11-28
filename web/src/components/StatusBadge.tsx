import { Badge } from "@/components/ui/badge";

interface StatusBadgeProps {
	status: string;
}

const statusConfig: Record<string, { className: string; dot: string }> = {
	running: {
		className: "bg-green text-white",
		dot: "bg-white",
	},
	deploying: {
		className: "bg-cyan text-white",
		dot: "bg-white",
	},
	stopped: {
		className: "bg-border-muted text-fg-default",
		dot: "bg-fg-default",
	},
	failed: {
		className: "bg-red text-white",
		dot: "bg-white",
	},
	pending: {
		className: "bg-yellow text-white",
		dot: "bg-white",
	},
};

export function StatusBadge({ status }: StatusBadgeProps) {
	const normalizedStatus = status.toLowerCase();
	const config = statusConfig[normalizedStatus] || statusConfig.pending;

	return (
		<Badge className={`${config.className} flex items-center gap-2`}>
			<span
				className={`w-2 h-2 rounded-full shrink-0 inline-block ${config.dot}`}
			></span>
			{status.charAt(0).toUpperCase() + status.slice(1)}
		</Badge>
	);
}
