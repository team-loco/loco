import { Badge } from "@/components/ui/badge";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import type { ResourceDomain } from "@/gen/loco/domain/v1/domain_pb";
import { getStatusLabel } from "@/lib/app-status";
import { ExternalLink } from "lucide-react";
import { useNavigate } from "react-router";
import { StatusBadge } from "./StatusBadge";
import { AppMenu } from "./dashboard/AppMenu";

interface AppCardProps {
	resource: Resource;
	onResourceDeleted?: () => void;
	workspaceId?: bigint;
}

function getPrimaryDomain(domains?: ResourceDomain[]): ResourceDomain | null {
	if (!domains || domains.length === 0) return null;
	return domains.find((d) => d.isPrimary) || domains[0];
}

export function AppCard({ resource, onResourceDeleted, workspaceId }: AppCardProps) {
	const navigate = useNavigate();

	const handleCardClick = () => {
		navigate(`/resource/${resource.id}${workspaceId ? `?workspace=${workspaceId}` : ""}`);
	};

	// Format resource type for display
	const resourceTypeLabel = resource.type || "SERVICE";

	// Format last deployed timestamp
	const getLastDeployedText = (): string => {
		if (!resource.createdAt) {
			return "never";
		}

		try {
			let timestamp: number;
			if (typeof resource.createdAt === "object" && "seconds" in resource.createdAt) {
				timestamp = Number(resource.createdAt.seconds) * 1000;
			} else if (typeof resource.createdAt === "number") {
				timestamp = resource.createdAt;
			} else {
				return "unknown";
			}

			const now = new Date().getTime();
			const diff = now - timestamp;
			const hours = Math.floor(diff / (1000 * 60 * 60));
			const days = Math.floor(diff / (1000 * 60 * 60 * 24));

			if (hours === 0) return "just now";
			if (hours === 1) return "1h ago";
			if (hours < 24) return `${hours}h ago`;
			if (days === 1) return "1d ago";
			return `${days}d ago`;
		} catch {
			return "unknown";
		}
	};

	return (
		<TooltipProvider>
			<div
				onClick={handleCardClick}
				className="group relative rounded-lg border border-neutral-200 dark:border-neutral-800 bg-background p-5 hover:border-neutral-300 dark:hover:border-neutral-700 hover:shadow-md transition-all cursor-pointer"
			>
				{/* Top section: Name and Status */}
				<div className="flex items-start justify-between gap-3 mb-4">
					<div className="flex-1 min-w-0">
						<h3 className="text-base font-semibold text-foreground truncate group-hover:text-accent transition-colors">
							{resource.name}
						</h3>
					</div>
					<div onClick={(e) => e.stopPropagation()} className="shrink-0">
						<AppMenu resource={resource} onResourceDeleted={onResourceDeleted} />
					</div>
				</div>

				{/* Middle section: Type and Status badges */}
				<div className="flex items-center gap-2 mb-4">
					<Badge variant="secondary" className="text-xs">
						{resourceTypeLabel}
					</Badge>
					<StatusBadge status={getStatusLabel(resource.status)} />
				</div>

			{/* Domain section */}
			{(() => {
				const primaryDomain = getPrimaryDomain(resource.domains);
				return (
					primaryDomain && (
						<div className="mb-4 flex items-center gap-2 group/link cursor-pointer hover:opacity-80 transition-opacity">
							<p className="text-sm text-foreground/70 truncate font-mono">
								{primaryDomain.domain}
							</p>
							<ExternalLink className="h-3.5 w-3.5 text-foreground/50 group-hover/link:text-foreground/70 shrink-0 transition-colors" />
						</div>
					)
				);
			})()}

				{/* Footer: Deployment info */}
				<div className="pt-3 border-t border-neutral-200 dark:border-neutral-800">
					<p className="text-xs text-foreground/50">
						Deployed {getLastDeployedText()}
					</p>
				</div>
			</div>
		</TooltipProvider>
	);
}
