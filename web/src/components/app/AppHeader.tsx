import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import { Copy, ExternalLink, Pencil } from "lucide-react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { getStatusLabel } from "@/lib/app-status";

interface AppHeaderProps {
	resource: Resource | null;
	isLoading?: boolean;
}

export function AppHeader({ resource, isLoading = false }: AppHeaderProps) {
	const navigate = useNavigate();

	if (isLoading) {
		return (
			<div className="rounded-lg border bg-card p-6 space-y-4 animate-pulse">
				<div className="h-8 bg-muted rounded w-1/3"></div>
				<div className="h-4 bg-muted rounded w-1/2"></div>
			</div>
		);
	}

	if (!resource) {
		return null;
	}

	const primaryDomain = resource.domains?.[0]?.domain;
	const resourceUrl = primaryDomain || "pending deployment";
	const resourceTypeLabel = resource.type || "SERVICE";
	const statusLabel = getStatusLabel(resource.status);
	const regions = resource.regions || [];

	const handleCopyUrl = () => {
		navigator.clipboard.writeText(`https://${resourceUrl}`);
		toast.success("URL copied to clipboard");
	};

	return (
		<TooltipProvider>
			<div className="rounded-lg border bg-card p-6 space-y-4">
				{/* Resource Name and Status */}
				<div className="flex items-start justify-between gap-4">
					<div>
						<div className="flex items-center gap-3 mb-2">
							<h1 className="text-3xl font-heading text-foreground">
								{resource.name}
							</h1>
							<Badge variant="secondary">{resourceTypeLabel}</Badge>
							<StatusBadge status={statusLabel} />
						</div>
					<p className="text-sm text-foreground opacity-70">
						{resource.name || "default"}
					</p>
				</div>
			</div>

			{/* URL and Regions */}
			<div className="flex flex-col gap-3 pt-4 border-t">
				<div className="flex flex-col sm:flex-row items-start sm:items-center gap-3">
					<div className="flex-1 flex items-center gap-2 break-all">
						<span className="text-sm text-foreground">https://{resourceUrl}</span>
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
						variant="outline"
						size="sm"
						onClick={() => window.open(`https://${resourceUrl}`, "_blank")}
						className="flex-1 sm:flex-none"
					>
						<ExternalLink className="w-4 h-4 mr-2" />
						Visit
					</Button>
					<Button
						variant="outline"
						size="sm"
						onClick={() => navigate(`/resource/${resource.id}/settings`)}
						className="flex-1 sm:flex-none"
					>
						<Pencil className="w-4 h-4 mr-2" />
						Edit
					</Button>
				</div>
				</div>

				{/* Regions */}
				{regions.length > 0 && (
					<div className="flex items-center gap-2">
						<span className="text-xs font-medium text-foreground opacity-70">
							Regions:
						</span>
						<div className="flex gap-2">
							{regions.map((region, idx) => (
								<Badge
									key={idx}
									variant="secondary"
									className="text-xs"
								>
									{region.region}
									{region.isPrimary && " (primary)"}
								</Badge>
							))}
						</div>
					</div>
				)}
			</div>
		</div>
		</TooltipProvider>
	);
}
