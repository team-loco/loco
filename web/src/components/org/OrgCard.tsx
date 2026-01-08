import { Building2, Settings, Trash2 } from "lucide-react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import type { Organization } from "@/gen/org/v1/org_pb";

interface OrgCardProps {
	org: Organization;
	workspaceCount?: number;
	onDelete?: (org: Organization) => void;
	onSwitch?: (orgId: bigint) => void;
}

export function OrgCard({
	org,
	workspaceCount = 0,
	onDelete,
	onSwitch,
}: OrgCardProps) {
	const navigate = useNavigate();

	const createdDate = org.createdAt?.seconds
		? new Date(Number(org.createdAt.seconds) * 1000).toLocaleDateString(
				"en-US",
				{
					year: "numeric",
					month: "short",
					day: "numeric",
				}
		  )
		: "Unknown";

	return (
		<Card className="hover:border-primary/50 transition-colors">
			<CardHeader>
				<div className="flex items-start justify-between">
					<div className="flex items-center gap-3">
						<div className="flex size-12 items-center justify-center rounded-lg bg-primary/10">
							<Building2 className="size-6 text-primary" />
						</div>
						<div>
							<CardTitle className="text-xl">{org.name}</CardTitle>
							<CardDescription className="mt-1">
								{workspaceCount} {workspaceCount === 1 ? "workspace" : "workspaces"} · Created {createdDate}
							</CardDescription>
						</div>
					</div>
				</div>
			</CardHeader>
			<CardContent>
				<div className="flex items-center gap-2">
					{onSwitch && (
						<Button
							variant="default"
							size="sm"
							onClick={() => onSwitch(org.id)}
						>
							View
						</Button>
					)}
					<Button
						variant="secondary"
						size="sm"
						onClick={() => navigate(`/org/${org.id}/settings`)}
					>
						<Settings className="size-4 mr-2" />
						Settings
					</Button>
					{onDelete && (
						<Button
							variant="ghost"
							size="sm"
							onClick={() => onDelete(org)}
							className="ml-auto text-destructive hover:text-destructive hover:bg-destructive/10"
						>
							<Trash2 className="size-4 mr-2" />
							Delete
						</Button>
					)}
				</div>
			</CardContent>
		</Card>
	);
}
