import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import type { Organization, OrgLimits, User } from "@gen/loco/org/v1/org_pb";
import { CircleArrowUpIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Badge } from "@/components/design/Badge";
import { LIMIT_TONE_TEXT } from "@/components/design/LimitedAction";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { useNow } from "@/hooks/useNow";
import { useOpenInvitations } from "@/hooks/useOpenInvitations";
import { formatCount } from "@/lib/format";
import { limitTone, limitUsage } from "@/lib/limits";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";

import { formatDay, SettingsCard, SettingsSection } from "./parts";

type LimitRow = {
	label: string;
	per: string;
	/** The proto field, which GetOrgLimits lists in `overridden` when this organization's value was raised. */
	field: string;
	pick: (limits: CountLimits) => number;
	/** The org-wide count against the limit; absent where the count is per workspace or resource. */
	used?: number | undefined;
};

function InUse({ row, limit }: { row: LimitRow; limit: number | undefined }) {
	if (row.used === undefined) return "—";
	const tone = limitTone(limitUsage(row.used, limit, row.label, ""));
	return <span className={cn(tone !== null && LIMIT_TONE_TEXT[tone])}>{formatCount(row.used)}</span>;
}

function Row({ row, limits }: { row: LimitRow; limits: OrgLimits | undefined }) {
	const limit = limits?.limits === undefined ? undefined : row.pick(limits.limits);
	return (
		<TableRow>
			<TableCell>{row.label}</TableCell>
			<TableCell className="text-fg2">{row.per}</TableCell>
			<TableCell className="font-mono text-fg2">
				<InUse row={row} limit={limit} />
			</TableCell>
			<TableCell className="text-right font-mono">
				<span className="inline-flex items-center justify-end gap-2">
					{limits?.overridden.includes(row.field) === true && (
						<Badge tone="info" size="sm">
							Raised
						</Badge>
					)}
					{limit === undefined ? "—" : formatCount(limit)}
				</span>
			</TableCell>
		</TableRow>
	);
}

function Rows({ rows, limits }: { rows: readonly LimitRow[]; limits: OrgLimits | undefined }) {
	return rows.map((row) => <Row key={row.field} row={row} limits={limits} />);
}

function RaisedBanner({ org, limits, users }: { org: Organization; limits: OrgLimits; users: User[] }): ReactNode {
	const by = users.find((u) => u.id === limits.updatedBy);
	return (
		<div className="mx-5 mt-3.5 flex items-start gap-2.5 rounded-lg border border-line bg-background px-3 py-2.5 text-[13px]">
			<CircleArrowUpIcon className="mt-0.5 size-[15px] shrink-0 text-info-fg" />
			<div className="min-w-0">
				<div>
					Raised for {org.name} by {by === undefined ? "a Loco administrator" : userLabel(by)} on{" "}
					{formatDay(limits.updatedAt)}
				</div>
				{limits.reason !== "" && <div className="mt-0.5 text-[12.5px] text-fg2">{limits.reason}</div>}
			</div>
		</div>
	);
}

export function LimitsSection({
	org,
	limits,
	users,
	workspaceCount,
	memberCount,
}: {
	org: Organization;
	limits: OrgLimits | undefined;
	users: User[];
	workspaceCount: number | undefined;
	memberCount: number | undefined;
}) {
	const openInvitations = useOpenInvitations(org.id, useNow().getTime());
	const limitRows: LimitRow[] = [
		{
			label: "Workspaces",
			per: "Organization",
			field: "workspaces_per_org",
			pick: (l) => l.workspacesPerOrg,
			used: workspaceCount,
		},
		{
			label: "Members",
			per: "Organization",
			field: "members_per_org",
			pick: (l) => l.membersPerOrg,
			used: memberCount,
		},
		{
			label: "Open invitations",
			per: "Organization",
			field: "invitations_per_org",
			pick: (l) => l.invitationsPerOrg,
			used: openInvitations,
		},
		{
			label: "Environments",
			per: "Workspace",
			field: "environments_per_workspace",
			pick: (l) => l.environmentsPerWorkspace,
		},
		{ label: "Resources", per: "Workspace", field: "resources_per_workspace", pick: (l) => l.resourcesPerWorkspace },
		{ label: "Webhooks", per: "Workspace", field: "webhooks_per_workspace", pick: (l) => l.webhooksPerWorkspace },
		{
			label: "API tokens",
			per: "User, organization, workspace or resource",
			field: "tokens_per_entity",
			pick: (l) => l.tokensPerEntity,
		},
		{ label: "Custom domains", per: "Resource", field: "domains_per_resource", pick: (l) => l.domainsPerResource },
		{ label: "Secrets", per: "Resource", field: "secrets_per_resource", pick: (l) => l.secretsPerResource },
	];
	const historyRows: LimitRow[] = [
		{
			label: "Deployments",
			per: "Resource, per environment",
			field: "deployment_history",
			pick: (l) => l.deploymentHistory,
		},
		{ label: "Builds", per: "Resource", field: "build_history", pick: (l) => l.buildHistory },
	];
	return (
		<SettingsSection title="Limits">
			<SettingsCard className="pb-1.5">
				{limits !== undefined && limits.overridden.length > 0 && (
					<RaisedBanner org={org} limits={limits} users={users} />
				)}
				<Table flush>
					<TableHeader>
						<TableRow>
							<TableHead>Limit</TableHead>
							<TableHead>Counted per</TableHead>
							<TableHead>In use</TableHead>
							<TableHead className="text-right">Limit</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						<Rows rows={limitRows} limits={limits} />
						<TableRow>
							<TableCell colSpan={4} className="pt-4 pb-1 text-[12.5px] text-fg3">
								History kept
							</TableCell>
						</TableRow>
						<Rows rows={historyRows} limits={limits} />
					</TableBody>
				</Table>
			</SettingsCard>
		</SettingsSection>
	);
}
