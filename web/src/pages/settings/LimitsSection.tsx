import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import type { Organization, OrgLimits, User } from "@gen/loco/org/v1/org_pb";
import { CircleArrowUpIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Badge } from "@/components/design/Badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { useNow } from "@/hooks/useNow";
import { useOpenInvitations } from "@/hooks/useOpenInvitations";
import { formatCount } from "@/lib/format";
import { userLabel } from "@/lib/user";

import { formatDay, SettingsCard, SettingsSection } from "./parts";

type LimitKey = { [K in keyof CountLimits]-?: CountLimits[K] extends number ? K : never }[keyof CountLimits];

type LimitRow = {
	key: LimitKey;
	label: string;
	per: string;
	/** The org-wide count against the limit; absent where the count is per workspace or resource. */
	used?: number | undefined;
};

/** GetOrgLimits lists a raised limit by its proto field name. */
const protoField = (key: LimitKey) => key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`);

function Rows({ rows, limits }: { rows: readonly LimitRow[]; limits: OrgLimits | undefined }) {
	return rows.map((row) => {
		const limit = limits?.limits?.[row.key];
		return (
			<TableRow key={row.key}>
				<TableCell>{row.label}</TableCell>
				<TableCell className="text-fg2">{row.per}</TableCell>
				<TableCell className="font-mono text-fg2">{row.used === undefined ? "—" : formatCount(row.used)}</TableCell>
				<TableCell className="text-right font-mono">
					<span className="inline-flex items-center justify-end gap-2">
						{limits?.overridden.includes(protoField(row.key)) === true && (
							<Badge tone="info" size="sm">
								Raised
							</Badge>
						)}
						{limit === undefined ? "—" : formatCount(limit)}
					</span>
				</TableCell>
			</TableRow>
		);
	});
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
		{ key: "workspacesPerOrg", label: "Workspaces", per: "Organization", used: workspaceCount },
		{ key: "membersPerOrg", label: "Members", per: "Organization", used: memberCount },
		{ key: "invitationsPerOrg", label: "Open invitations", per: "Organization", used: openInvitations },
		{ key: "environmentsPerWorkspace", label: "Environments", per: "Workspace" },
		{ key: "resourcesPerWorkspace", label: "Resources", per: "Workspace" },
		{ key: "webhooksPerWorkspace", label: "Webhooks", per: "Workspace" },
		{ key: "tokensPerEntity", label: "API tokens", per: "User, organization, workspace or resource" },
		{ key: "domainsPerResource", label: "Custom domains", per: "Resource" },
		{ key: "secretsPerResource", label: "Secrets", per: "Resource" },
	];
	const historyRows: LimitRow[] = [
		{ key: "deploymentHistory", label: "Deployments", per: "Resource, per environment" },
		{ key: "buildHistory", label: "Builds", per: "Resource" },
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
