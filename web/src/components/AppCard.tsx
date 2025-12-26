import { Badge } from "@/components/ui/badge";
import type { Resource } from "@/gen/resource/v1/resource_pb";
import type { ResourceDomain } from "@/gen/domain/v1/domain_pb";
import { getStatusLabel } from "@/lib/app-status";
import { ExternalLink } from "lucide-react";
import { useNavigate } from "react-router";
import { StatusBadge } from "./StatusBadge";
import { AppMenu } from "./dashboard/AppMenu";

interface AppCardProps {
	app: Resource;
	onAppDeleted?: () => void;
	workspaceId?: bigint;
}

function getPrimaryDomain(domains?: ResourceDomain[]): ResourceDomain | null {
	if (!domains || domains.length === 0) return null;
	return domains.find((d) => d.isPrimary) || domains[0];
}

export function AppCard({ app, onAppDeleted, workspaceId }: AppCardProps) {
	const navigate = useNavigate();

	const handleCardClick = () => {
		navigate(`/app/${app.id}${workspaceId ? `?workspace=${workspaceId}` : ""}`);
	};

	// Format app type for display
	const appTypeLabel = app.type || "SERVICE";

	// Format last deployed timestamp
	const getLastDeployedText = (): string => {
		if (!app.createdAt) {
			return "never";
		}

		try {
			let timestamp: number;
			if (typeof app.createdAt === "object" && "seconds" in app.createdAt) {
				timestamp = Number(app.createdAt.seconds) * 1000;
			} else if (typeof app.createdAt === "number") {
				timestamp = app.createdAt;
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
		<div
			onClick={handleCardClick}
			className="group relative rounded-lg border border-neutral-200 dark:border-neutral-800 bg-background p-5 hover:border-neutral-300 dark:hover:border-neutral-700 hover:shadow-md transition-all cursor-pointer"
		>
			{/* Top section: Name and Status */}
			<div className="flex items-start justify-between gap-3 mb-4">
				<div className="flex-1 min-w-0">
					<h3 className="text-base font-semibold text-foreground truncate group-hover:text-accent transition-colors">
						{app.name}
					</h3>
				</div>
				<div onClick={(e) => e.stopPropagation()} className="shrink-0">
					<AppMenu app={app} onAppDeleted={onAppDeleted} />
				</div>
			</div>

			{/* Middle section: Type and Status badges */}
			<div className="flex items-center gap-2 mb-4">
				<Badge variant="secondary" className="text-xs">
					{appTypeLabel}
				</Badge>
				<StatusBadge status={getStatusLabel(app.status)} />
			</div>

			{/* Domain section */}
			{(() => {
				const primaryDomain = getPrimaryDomain(app.domains);
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
	);
}
