import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import type { App } from "@/gen/app/v1/app_pb";
import { useNavigate } from "react-router";
import { StatusBadge } from "./StatusBadge";
import { AppMenu } from "./dashboard/AppMenu";

interface AppCardProps {
	app: App;
	onAppDeleted?: () => void;
	workspaceId?: bigint;
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
		<Card
			className="cursor-pointer hover:shadow-neo transition-shadow"
			onClick={handleCardClick}
		>
			<CardContent className="p-6 space-y-4">
				{/* Header: Name and Menu */}
				<div className="flex items-start justify-between gap-2">
					<div className="flex-1 min-w-0">
						<h3 className="text-lg font-heading text-foreground truncate">
							{app.name}
						</h3>
						<p className="text-sm text-foreground opacity-70 mt-1 truncate">
							{app.domain?.domain || "no domain"}
						</p>
					</div>
					<div onClick={(e) => e.stopPropagation()}>
						<AppMenu app={app} onAppDeleted={onAppDeleted} />
					</div>
				</div>

				{/* Type Badge and Status */}
				<div className="flex items-center justify-between gap-2">
					<Badge variant="secondary">{appTypeLabel}</Badge>
					<StatusBadge status="running" />
				</div>

				{/* Metadata */}
				<div className="text-xs text-foreground opacity-60 space-y-1">
					<p>Deployed {getLastDeployedText()}</p>
				</div>

				{/* Domain Info */}
				<p className="text-xs text-foreground opacity-50 border-t border-border pt-3 mt-3 truncate">
					{app.domain?.domain || "pending deployment"}
				</p>
			</CardContent>
		</Card>
	);
}
