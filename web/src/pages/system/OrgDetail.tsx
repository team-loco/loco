import { useQuery } from "@connectrpc/connect-query";
import { listOrgMembers, listOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgMember } from "@gen/loco/org/v1/org_pb";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { ExternalLinkIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useParams } from "react-router";

import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { CopyId } from "@/components/design/CopyId";
import { Page, PageHeader, PageLoading, Section } from "@/components/design/Page";
import { useMyScopes } from "@/hooks/useMyScopes";
import { formatCount } from "@/lib/format";
import { notFound } from "@/lib/not-found";
import { workspacePath } from "@/lib/routes";
import { systemAccess } from "@/lib/system";
import { userLabel } from "@/lib/user";

import { OrgSuspensionButton } from "./OrgActions";
import { OrgLimitsSection } from "./OrgLimits";
import { OrgStateBadge, SystemDate, SystemTime } from "./shared";

const MEMBER_PAGE = 200;

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<dt className="text-[12.5px] text-fg3">{label}</dt>
			<dd className="m-0 min-w-0 text-[13.5px]">{children}</dd>
		</div>
	);
}

const MEMBER_COLUMNS: DataColumn<OrgMember>[] = [
	{
		id: "name",
		header: "Member",
		size: 220,
		minSize: 120,
		meta: { primary: true, fit: true, fitMax: 280 },
		cell: ({ row: { original: m } }) => <CellText text={m.user === undefined ? "" : userLabel(m.user)} />,
	},
	{
		id: "email",
		header: "Email",
		size: 260,
		minSize: 140,
		meta: { grow: true, hideOnMobile: true },
		cell: ({ row: { original: m } }) => <CellText text={m.user?.email ?? ""} className="text-fg2" />,
	},
	{
		id: "role",
		header: "Role",
		size: 100,
		minSize: 80,
		cell: ({ row: { original: m } }) => (m.scopes.includes("admin") ? "Admin" : "Member"),
	},
	{
		id: "joined",
		header: "Joined",
		size: 140,
		minSize: 100,
		meta: { hideOnMobile: true, cellClassName: "whitespace-nowrap text-[12.5px] text-fg3" },
		cell: ({ row: { original: m } }) => <SystemDate ts={m.joinedAt} />,
	},
];

function Members({ orgId, total }: { orgId: string; total: number | undefined }) {
	const members = useQuery(listOrgMembers, { orgId, pageSize: MEMBER_PAGE });
	const rows = members.data?.members ?? [];
	const table = useDataTable("system-org-members", {
		columns: MEMBER_COLUMNS,
		data: rows,
		getRowId: (m) => m.user?.id ?? "",
	});
	return (
		<Section
			title="Members"
			count={total === undefined ? undefined : formatCount(total)}
			meta={members.data?.nextPageToken ? `Showing the first ${MEMBER_PAGE}` : undefined}
		>
			{members.error !== null ? (
				<ErrorCard error={members.error} fallbackMessage="Failed to load the members" minHeight="min-h-32" />
			) : (
				<DataTable table={table} label="Members" band loading={members.isPending} />
			)}
		</Section>
	);
}

export function SystemOrgDetail() {
	const { orgId = "" } = useParams();
	const admin = systemAccess(useMyScopes().scopes) === "admin";
	const found = useQuery(listOrgs, { query: orgId, pageSize: 5 });
	const firstWorkspace = useQuery(listOrgWorkspaces, { orgId, pageSize: 1 });
	if (found.isPending) return <PageLoading />;
	if (found.error !== null) {
		return <ErrorCard error={found.error} fallbackMessage="Failed to load the organization" minHeight="min-h-48" />;
	}
	const summary = found.data.orgs.find((s) => s.organization?.id === orgId);
	const org = summary?.organization;
	if (summary === undefined || org === undefined) notFound();
	const workspaceId = firstWorkspace.data?.workspaces[0]?.id;
	return (
		<Page>
			<PageHeader
				title={
					<>
						{org.name}
						<OrgStateBadge org={org} />
					</>
				}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						{workspaceId !== undefined && (
							<Button
								variant="outline"
								size="sm"
								nativeButton={false}
								render={<Link to={workspacePath(org.id, workspaceId)} />}
							>
								<ExternalLinkIcon />
								Open workspace
							</Button>
						)}
						{admin && <OrgSuspensionButton org={org} />}
					</div>
				}
			/>
			{org.suspendedAt !== undefined && (
				<p className="m-0 text-[13.5px] text-fg2">
					Suspended since <SystemTime ts={org.suspendedAt} />. Reason: {org.suspensionReason}
				</p>
			)}
			<Section title="Details">
				<dl className="m-0 grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-x-6 gap-y-5 p-5">
					<Fact label="Owners">
						{summary.owners.length === 0 ? (
							<span className="text-fg3">None</span>
						) : (
							summary.owners.map((o) => (
								<div key={o.id} className="truncate" title={o.email}>
									{userLabel(o)}
								</div>
							))
						)}
					</Fact>
					<Fact label="Created">
						<SystemDate ts={org.createdAt} />
					</Fact>
					<Fact label="Members">{formatCount(Number(org.memberCount ?? 0))}</Fact>
					<Fact label="Workspaces">{formatCount(Number(org.workspaceCount ?? 0))}</Fact>
					<Fact label="Resources">{formatCount(Number(summary.resourceCount))}</Fact>
					<Fact label="Organization ID">
						<CopyId value={org.id} className="-ml-1.5">
							<span className="font-mono text-[12.5px]">{org.id}</span>
						</CopyId>
					</Fact>
				</dl>
			</Section>
			<OrgLimitsSection orgId={org.id} admin={admin} />
			<Members orgId={org.id} total={org.memberCount === undefined ? undefined : Number(org.memberCount)} />
		</Page>
	);
}
