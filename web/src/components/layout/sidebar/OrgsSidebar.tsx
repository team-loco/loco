import { useQuery } from "@connectrpc/connect-query";
import { listOrgWorkspaces } from "@/gen/loco/workspace/v1";
import { ChevronDown } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import type { Organization } from "@/gen/loco/org/v1/org_pb";

interface OrgsSidebarProps {
	orgs: Organization[];
	expandedOrgs: Set<bigint>;
	onExpandOrg: (orgId: bigint) => void;
	onWorkspaceClick: (workspaceId: bigint) => void;
	onWorkspaceNameChange: (name: string | null) => void;
	activeWorkspaceId: bigint | null;
}

export function OrgsSidebar({
	orgs,
	expandedOrgs,
	onExpandOrg,
	onWorkspaceClick,
	onWorkspaceNameChange,
	activeWorkspaceId,
}: OrgsSidebarProps) {
	return (
		<div className="space-y-1">
			{orgs.length === 0 ? (
				<>
					<Skeleton className="h-8 w-full rounded-lg" />
					<Skeleton className="h-8 w-full rounded-lg" />
				</>
			) : (
				orgs.map((org) => (
					<OrgItem
						key={org.id.toString()}
						org={org}
						isExpanded={expandedOrgs.has(org.id)}
						onExpand={() => onExpandOrg(org.id)}
						onWorkspaceClick={onWorkspaceClick}
						onWorkspaceNameChange={onWorkspaceNameChange}
						activeWorkspaceId={activeWorkspaceId}
					/>
				))
			)}
		</div>
	);
}

interface OrgItemProps {
	org: Organization;
	isExpanded: boolean;
	onExpand: () => void;
	onWorkspaceClick: (workspaceId: bigint) => void;
	onWorkspaceNameChange: (name: string | null) => void;
	activeWorkspaceId: bigint | null;
}

function OrgItem({
	org,
	isExpanded,
	onExpand,
	onWorkspaceClick,
	onWorkspaceNameChange,
	activeWorkspaceId,
}: OrgItemProps) {
	const { data: workspacesRes, isLoading } = useQuery(
		listOrgWorkspaces,
		{ orgId: org.id },
		{ enabled: isExpanded }
	);

	const workspaces = workspacesRes?.workspaces ?? [];

	// Update parent with the workspace name when active workspace changes
	if (activeWorkspaceId) {
		const activeWorkspace = workspaces.find((ws) => ws.id === activeWorkspaceId);
		if (activeWorkspace && activeWorkspace.name) {
			onWorkspaceNameChange(activeWorkspace.name);
		}
	}

	return (
		<div key={org.id.toString()} className="space-y-1">
			<button
				onClick={onExpand}
				className="w-full flex items-center justify-between px-3 py-2 rounded-lg text-sm font-base hover:bg-secondary-background transition-colors"
			>
				<span className="font-heading truncate">{org.name}</span>
				<ChevronDown
					className={`h-4 w-4 transition-transform ${
						isExpanded ? "rotate-180" : ""
					}`}
				/>
			</button>

			{isExpanded && (
				<div className="pl-4 space-y-1">
					{isLoading ? (
						<>
							<Skeleton className="h-7 w-full rounded-lg" />
							<Skeleton className="h-7 w-full rounded-lg" />
						</>
					) : (
						workspaces.map((ws) => (
							<button
								key={ws.id.toString()}
								onClick={() => {
									onWorkspaceClick(ws.id);
									onWorkspaceNameChange(ws.name);
								}}
								className={`w-full text-left px-3 py-1.5 rounded-lg text-sm transition-colors ${
									activeWorkspaceId === ws.id
										? "bg-main text-main-foreground"
										: "hover:bg-secondary-background"
								}`}
							>
								<span className="truncate block">{ws.name}</span>
							</button>
						))
					)}
				</div>
			)}
		</div>
	);
}
