import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { App } from "@/gen/app/v1/app_pb";
import { Copy, ExternalLink, Pencil } from "lucide-react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

interface AppHeaderProps {
	app: App | null;
	isLoading?: boolean;
}

export function AppHeader({ app, isLoading = false }: AppHeaderProps) {
	const navigate = useNavigate();

	if (isLoading) {
		return (
			<div className="bg-background border-2 border-border rounded-neo p-6 space-y-4 animate-pulse">
				<div className="h-8 bg-main/20 rounded w-1/3"></div>
				<div className="h-4 bg-main/10 rounded w-1/2"></div>
			</div>
		);
	}

	if (!app) {
		return null;
	}

	const appUrl = app.domain || `${app.subdomain}.deploy-app.com`;
	const appTypeLabel = app.type || "SERVICE";

	const handleCopyUrl = () => {
		navigator.clipboard.writeText(`https://${appUrl}`);
		toast.success("URL copied to clipboard");
	};

	return (
		<div className="bg-background border-2 border-border rounded-neo p-6 space-y-4">
			{/* App Name and Status */}
			<div className="flex items-start justify-between gap-4">
				<div>
					<div className="flex items-center gap-3 mb-2">
						<h1 className="text-3xl font-heading text-foreground">
							{app.name}
						</h1>
						<Badge variant="neutral">{appTypeLabel}</Badge>
						<StatusBadge status="running" />
					</div>
					<p className="text-sm text-foreground opacity-70">
						{app.namespace || "default"}
					</p>
				</div>
			</div>

			{/* URL and Actions */}
			<div className="flex flex-col sm:flex-row items-start sm:items-center gap-3 pt-4 border-t border-border">
				<div className="flex-1 flex items-center gap-2 break-all">
					<span className="text-sm text-foreground">https://{appUrl}</span>
					<Button
						size="sm"
						onClick={handleCopyUrl}
						className="h-6 w-6 p-0"
						aria-label="Copy URL"
					>
						<Copy className="w-4 h-4" />
					</Button>
				</div>

				<div className="flex items-center gap-2 w-full sm:w-auto">
					<Button
						variant="noShadow"
						size="sm"
						onClick={() => window.open(`https://${appUrl}`, "_blank")}
						className="border-2 flex-1 sm:flex-none"
					>
						<ExternalLink className="w-4 h-4 mr-2" />
						Visit
					</Button>
					<Button
						variant="noShadow"
						size="sm"
						onClick={() => navigate(`/app/${app.id}/settings`)}
						className="border-2 flex-1 sm:flex-none"
					>
						<Pencil className="w-4 h-4 mr-2" />
						Edit
					</Button>
				</div>
			</div>
		</div>
	);
}
