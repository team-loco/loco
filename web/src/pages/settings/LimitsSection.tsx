import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import type { Organization, OrgLimits } from "@gen/loco/org/v1/org_pb";

import { Badge } from "@/components/design/Badge";
import { LIMIT_TONE_TEXT } from "@/components/design/LimitedAction";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { useOpenInvitations } from "@/hooks/useOpenInvitations";
import { formatCount } from "@/lib/format";
import { limitTone, limitUsage } from "@/lib/limits";
import { cn } from "@/lib/utils";

import { SettingsCard, SettingsSection } from "./parts";

type LimitKey = { [K in keyof CountLimits]-?: CountLimits[K] extends number ? K : never }[keyof CountLimits];

type LimitRow = {
	key: LimitKey;
	label: string;
	per: string;
	/** The count against the limit, org-wide or in `where`; absent where nothing is counted. */
	used?: number | undefined;
	/** The workspace a per-workspace count is from. */
	where?: string | undefined;
	unit?: string;
};

/** GetOrgLimits lists a raised limit by its proto field name. */
const protoField = (key: LimitKey) => key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`);

interface Row {
	label: string;
	per: string;
	used: string;
	usedClass: string;
	value: string;
	raised: boolean;
	group: string | undefined;
}

function limitRow(row: LimitRow, limits: OrgLimits | undefined, group?: string): Row {
	const limit = limits?.limits?.[row.key];
	const tone = row.used === undefined ? null : limitTone(limitUsage(row.used, limit, row.label, ""));
	return {
		label: row.label,
		per: row.per,
		used: row.used === undefined ? "—" : `${formatCount(row.used)}${row.where === undefined ? "" : ` in ${row.where}`}`,
		usedClass: tone === null ? "text-fg2" : LIMIT_TONE_TEXT[tone],
		value: limit === undefined ? "—" : `${formatCount(limit)}${row.unit === undefined ? "" : ` ${row.unit}`}`,
		raised: limits?.overridden.includes(protoField(row.key)) === true,
		group,
	};
}

const HISTORY = "History kept";

function retentionRow(label: string, per: string, value: string): Row {
	return { label, per, used: "—", usedClass: "text-fg2", value, raised: false, group: HISTORY };
}

const COLUMNS: DataColumn<Row>[] = [
	{
		id: "label",
		header: "Limit",
		size: 200,
		minSize: 100,
		meta: { fit: true },
		cell: ({ row: { original: row } }) => <CellText text={row.label} />,
	},
	{
		id: "per",
		header: "Counted per",
		size: 200,
		minSize: 100,
		meta: { grow: true },
		cell: ({ row: { original: row } }) => <CellText text={row.per} className="text-fg2" />,
	},
	{
		id: "used",
		header: "In use",
		size: 110,
		minSize: 72,
		meta: { fit: true },
		cell: ({ row: { original: row } }) => <span className={cn("font-mono", row.usedClass)}>{row.used}</span>,
	},
	{
		id: "limit",
		header: "Limit",
		size: 160,
		minSize: 100,
		meta: { align: "end", fit: true },
		cell: ({ row: { original: row } }) => (
			<span className="inline-flex items-center justify-end gap-2 font-mono whitespace-nowrap">
				{row.raised && (
					<Badge tone="info" size="sm">
						Raised
					</Badge>
				)}
				{row.value}
			</span>
		),
	},
];

export function LimitsSection({
	org,
	limits,
	workspaceCount,
	memberCount,
	workspace,
}: {
	org: Organization;
	limits: OrgLimits | undefined;
	workspaceCount: number | undefined;
	memberCount: number | undefined;
	/** The workspace the page is in, whose per-workspace counts show beside their limits. */
	workspace: { name: string; resources: number | undefined } | undefined;
}) {
	const { data: config } = useQuery(getConfig, {});
	const retention = config?.historyRetention;
	const eventDays =
		retention?.events === undefined
			? undefined
			: (Number(retention.events.seconds) + retention.events.nanos / 1e9) / 86_400;
	const openInvitations = useOpenInvitations(org.id);
	const limitRows: LimitRow[] = [
		{ key: "workspacesPerOrg", label: "Workspaces", per: "Organization", used: workspaceCount },
		{ key: "membersPerOrg", label: "Members", per: "Organization", used: memberCount },
		{ key: "invitationsPerOrg", label: "Open invitations", per: "Organization", used: openInvitations },
		{ key: "environmentsPerWorkspace", label: "Environments", per: "Workspace" },
		{
			key: "resourcesPerWorkspace",
			label: "Resources",
			per: "Workspace",
			used: workspace?.resources,
			where: workspace?.name,
		},
		{ key: "webhooksPerWorkspace", label: "Webhooks", per: "Workspace" },
		{ key: "tokensPerEntity", label: "API tokens", per: "User, organization, workspace or resource" },
		{ key: "domainsPerResource", label: "Custom domains", per: "Resource" },
		{ key: "secretsPerEnvironment", label: "Secrets", per: "Environment" },
	];
	const rows: Row[] = [
		...limitRows.map((row) => limitRow(row, limits)),
		limitRow({ key: "deploymentHistory", label: "Deployments", per: "Resource, per environment" }, limits, HISTORY),
		limitRow({ key: "buildHistory", label: "Builds", per: "Resource" }, limits, HISTORY),
		retentionRow(
			"Build images",
			"Resource, plus images in use",
			retention === undefined
				? "—"
				: retention.buildImagesPerResource === 0
					? "Cleanup disabled"
					: formatCount(retention.buildImagesPerResource),
		),
		retentionRow(
			"Events and webhook deliveries",
			"Organization",
			eventDays === undefined
				? "—"
				: `${new Intl.NumberFormat(undefined, { maximumSignificantDigits: 9 }).format(eventDays)} days`,
		),
		limitRow({ key: "logRetentionDays", label: "Logs", per: "Organization", unit: "days" }, limits, HISTORY),
		limitRow({ key: "traceRetentionDays", label: "Traces", per: "Organization", unit: "days" }, limits, HISTORY),
		limitRow({ key: "metricRetentionDays", label: "Metrics", per: "Organization", unit: "days" }, limits, HISTORY),
		limitRow(
			{ key: "expiredTokenRetentionDays", label: "Expired API tokens", per: "Organization", unit: "days" },
			limits,
			HISTORY,
		),
	];
	const table = useDataTable("limits", {
		columns: COLUMNS,
		data: rows,
		getRowId: (row) => row.label,
	});
	return (
		<SettingsSection id="s-lim" title="Limits">
			<SettingsCard>
				<DataTable table={table} label="Limits" className="[&_tr[data-group]]:border-t" groupBy={(row) => row.group} />
			</SettingsCard>
		</SettingsSection>
	);
}
