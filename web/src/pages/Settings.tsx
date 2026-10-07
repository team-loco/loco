import { useQuery } from "@connectrpc/connect-query";
import { getOrg, listOrgUsers } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { Building2Icon, LayersIcon, ScrollTextIcon } from "lucide-react";
import { useParams, useSearchParams } from "react-router";

import { Page, PageHeader } from "@/components/design/Page";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useMyScopes } from "@/hooks/useMyScopes";
import { getErrorMessage } from "@/lib/error-handler";

import { AuditLogTab } from "./settings/AuditLogTab";
import { OrgTab } from "./settings/OrgTab";
import { useResourceCounts } from "./settings/useResourceCounts";
import { SettingsSkeleton, WorkspaceTab } from "./settings/WorkspaceTab";

type Tab = "ws" | "org" | "audit";

const TABS: readonly Tab[] = ["ws", "org", "audit"];

function tabOf(value: string | null | undefined): Tab | undefined {
	return TABS.find((t) => t === value);
}

export function Settings() {
	const { orgId = "", workspaceId = "" } = useParams<{ orgId: string; workspaceId: string }>();
	const [params, setParams] = useSearchParams();
	const requested = tabOf(params.get("tab")) ?? "ws";

	const orgQuery = useQuery(getOrg, { key: { case: "orgId", value: orgId } }, { enabled: orgId !== "" });
	const wsQuery = useQuery(listOrgWorkspaces, { orgId, pageSize: 200 }, { enabled: orgId !== "" });
	const usersQuery = useQuery(listOrgUsers, { orgId, pageSize: 200 }, { enabled: orgId !== "" });
	const scopes = useMyScopes();

	const org = orgQuery.data?.organization;
	const workspaces = wsQuery.data?.workspaces ?? [];
	const users = usersQuery.data?.users ?? [];
	const workspaceIds = workspaces.map((w) => w.id);
	const { counts, isLoading: countsLoading } = useResourceCounts(workspaceIds);
	const currentWs = workspaces.find((w) => w.id === workspaceId);
	const canAudit = org !== undefined && scopes.orgLevel(org.id) >= 3;
	const tab: Tab = requested === "audit" && !canAudit ? "org" : requested;

	const selectTab = (next: Tab) => {
		const p = new URLSearchParams(params);
		p.set("tab", next);
		setParams(p, { replace: true });
	};

	const orgError = orgQuery.error ? getErrorMessage(orgQuery.error, "Failed to load organization") : null;

	return (
		<Page className="max-w-[1080px] gap-5">
			<PageHeader title="Settings" />
			<ToggleGroup
				variant="segmented"
				className="max-w-full"
				value={[tab]}
				onValueChange={(v: string[]) => {
					const next = tabOf(v[0]);
					if (next !== undefined) selectTab(next);
				}}
			>
				<ToggleGroupItem value="ws" className="h-7! min-w-0 shrink gap-1.5 px-3 text-[12.5px]">
					<LayersIcon className="size-[13px] shrink-0" />
					<span className="truncate">{currentWs?.name ?? "Workspace"}</span>
				</ToggleGroupItem>
				<ToggleGroupItem value="org" className="h-7! min-w-0 shrink gap-1.5 px-3 text-[12.5px]">
					<Building2Icon className="size-[13px] shrink-0" />
					<span className="truncate">{org?.name ?? "Organization"}</span>
				</ToggleGroupItem>
				{canAudit && (
					<ToggleGroupItem value="audit" className="h-7! shrink-0 gap-1.5 px-3 text-[12.5px]">
						<ScrollTextIcon className="size-[13px] shrink-0" />
						Audit log
					</ToggleGroupItem>
				)}
			</ToggleGroup>

			{orgError !== null && <div className="rounded-lg border border-line px-5 py-8 text-fg3">{orgError}</div>}
			{orgError === null && !org && <SettingsSkeleton />}
			{org && tab === "ws" && (
				<WorkspaceTab
					key={workspaceId}
					orgId={org.id}
					orgName={org.name}
					workspaceId={workspaceId}
					workspaces={workspaces}
					users={users}
					level={scopes.workspaceLevel(org.id, workspaceId)}
					resourceCount={counts.get(workspaceId)}
				/>
			)}
			{org && tab === "org" && (
				<OrgTab
					key={org.id}
					org={org}
					workspaces={workspaces}
					workspacesLoading={wsQuery.isLoading}
					users={users}
					level={scopes.orgLevel(org.id)}
					counts={counts}
					countsLoading={countsLoading}
				/>
			)}
			{org && tab === "audit" && <AuditLogTab key={org.id} orgId={org.id} />}
		</Page>
	);
}
