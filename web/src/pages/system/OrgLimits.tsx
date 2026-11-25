import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { CountLimitsSchema } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits, setOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgLimits } from "@gen/loco/org/v1/org_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ErrorCard } from "@/components/ErrorCard";
import { Badge } from "@/components/design/Badge";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { RowMenu } from "@/components/design/RowMenu";
import { toastConnectError } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { protoField, type LimitKey } from "@/lib/limits";
import { toast } from "@/lib/toast";

import { ReasonDialog } from "./ReasonDialog";
import { refreshOrgs, SystemTime } from "./shared";

interface LimitRow {
	key: LimitKey;
	label: string;
	unit?: string;
}

/** The limits an organization can override. */
const LIMITS: LimitRow[] = [
	{ key: "workspacesPerOrg", label: "Workspaces per organization" },
	{ key: "membersPerOrg", label: "Members per organization" },
	{ key: "invitationsPerOrg", label: "Open invitations per organization" },
	{ key: "environmentsPerWorkspace", label: "Environments per workspace" },
	{ key: "resourcesPerWorkspace", label: "Resources per workspace" },
	{ key: "webhooksPerWorkspace", label: "Webhooks per workspace" },
	{ key: "tokensPerEntity", label: "API tokens per user, organization, workspace or resource" },
	{ key: "domainsPerResource", label: "Custom domains per resource and environment" },
	{ key: "secretsPerEnvironment", label: "Secrets per environment" },
	{ key: "deploymentHistory", label: "Deployments kept per resource and environment" },
	{ key: "buildHistory", label: "Builds kept per resource" },
	{ key: "logRetentionDays", label: "Log retention", unit: "days" },
	{ key: "traceRetentionDays", label: "Trace retention", unit: "days" },
	{ key: "metricRetentionDays", label: "Metric retention", unit: "days" },
	{ key: "expiredTokenRetentionDays", label: "Expired API tokens kept", unit: "days" },
];

interface Row extends LimitRow {
	value: number;
	fallback: number | undefined;
	overridden: boolean;
}

function amount(value: number | undefined, unit: string | undefined): string {
	if (value === undefined) return "—";
	return unit === undefined ? formatCount(value) : `${formatCount(value)} ${unit}`;
}

type Change = { row: Row; kind: "override" | "default" };

function columns(admin: boolean, onChange: (change: Change) => void): DataColumn<Row>[] {
	return [
		{
			id: "label",
			header: "Limit",
			size: 320,
			minSize: 160,
			meta: { grow: true, primary: true },
			cell: ({ row: { original: r } }) => <CellText text={r.label} />,
		},
		{
			id: "value",
			header: "Value",
			size: 130,
			minSize: 90,
			meta: { align: "end", cellClassName: "font-mono text-[12.5px]" },
			cell: ({ row: { original: r } }) => amount(r.value, r.unit),
		},
		{
			id: "source",
			header: "Set by",
			size: 190,
			minSize: 130,
			cell: ({ row: { original: r } }) =>
				r.overridden ? (
					<Badge tone="info" size="sm">
						Override
					</Badge>
				) : (
					<span className="text-fg3">Install default</span>
				),
		},
		{
			id: "default",
			header: "Install default",
			size: 140,
			minSize: 100,
			meta: { hideOnMobile: true, align: "end", cellClassName: "font-mono text-[12.5px] text-fg3" },
			cell: ({ row: { original: r } }) => amount(r.fallback, r.unit),
		},
		...(admin
			? [
					{
						id: "menu",
						meta: { pinEnd: true, label: "Actions" },
						cell: ({ row: { original: r } }: { row: { original: Row } }) => (
							<RowMenu label={`Actions for ${r.label}`}>
								<DropdownMenuItem
									onClick={() => {
										onChange({ row: r, kind: "override" });
									}}
								>
									{r.overridden ? "Change override" : "Override"}
								</DropdownMenuItem>
								{r.overridden && (
									<DropdownMenuItem
										onClick={() => {
											onChange({ row: r, kind: "default" });
										}}
									>
										Return to install default
									</DropdownMenuItem>
								)}
							</RowMenu>
						),
					} satisfies DataColumn<Row>,
				]
			: []),
	];
}

function ChangeDialog({ orgId, change, onClose }: { orgId: string; change: Change | null; onClose: () => void }) {
	const queryClient = useQueryClient();
	const save = useMutation(setOrgLimits);
	const [text, setText] = useState("");
	const row = change?.row;
	const overriding = change?.kind === "override";
	const value = Number(text);
	const valid = Number.isInteger(value) && value > 0;

	const submit = async (reason: string) => {
		if (row === undefined) return;
		const limits = create(CountLimitsSchema);
		limits[row.key] = overriding ? value : 0;
		try {
			await save.mutateAsync({
				orgId,
				limits,
				updateMask: create(FieldMaskSchema, { paths: [protoField(row.key)] }),
				reason,
			});
			toast(
				overriding
					? `Overrode ${row.label.toLowerCase()}`
					: `Returned ${row.label.toLowerCase()} to the install default`,
			);
			onClose();
			await refreshOrgs(queryClient);
		} catch (err) {
			toastConnectError(err, "Failed to change the limit");
		}
	};

	return (
		<ReasonDialog
			open={change !== null}
			title={
				row === undefined
					? ""
					: overriding
						? `Override ${row.label.toLowerCase()}`
						: `Return ${row.label.toLowerCase()} to the install default`
			}
			description={
				row === undefined
					? ""
					: overriding
						? `The install default is ${amount(row.fallback, row.unit)}. The override applies to this organization only.`
						: `The organization uses the install default of ${amount(row.fallback, row.unit)} again.`
			}
			confirmLabel={overriding ? "Override limit" : "Return to default"}
			canSubmit={!overriding || valid}
			pending={save.isPending}
			onSubmit={(reason) => {
				void submit(reason);
			}}
			onClose={onClose}
		>
			{row !== undefined && overriding && (
				<Field label={row.unit === undefined ? "New limit" : `New limit (${row.unit})`}>
					<Input
						type="number"
						min={1}
						step={1}
						value={text}
						onChange={(e) => {
							setText(e.target.value);
						}}
					/>
				</Field>
			)}
		</ReasonDialog>
	);
}

function latestChange(limits: OrgLimits): string | null {
	if (limits.overridden.length === 0) return null;
	return limits.reason === "" ? null : limits.reason;
}

/** The organization's limits with where each value comes from; admins override them. */
export function OrgLimitsSection({ orgId, admin }: { orgId: string; admin: boolean }) {
	const limits = useQuery(getOrgLimits, { orgId });
	const config = useQuery(getConfig, {});
	const [change, setChange] = useState<Change | null>(null);
	const current = limits.data?.limits;
	const rows: Row[] = LIMITS.map((l) => ({
		...l,
		value: current?.limits?.[l.key] ?? 0,
		fallback: config.data?.countLimits?.[l.key],
		overridden: current?.overridden.includes(protoField(l.key)) === true,
	}));
	const table = useDataTable("system-org-limits", {
		columns: columns(admin, setChange),
		data: rows,
		getRowId: (r) => r.key,
	});
	const reason = current === undefined ? null : latestChange(current);
	return (
		<Section
			title="Limits"
			meta={
				reason === null || current === undefined ? undefined : (
					<>
						Latest change: {reason}, <SystemTime ts={current.updatedAt} />
					</>
				)
			}
		>
			{limits.error !== null ? (
				<ErrorCard error={limits.error} fallbackMessage="Failed to load the limits" minHeight="min-h-32" />
			) : (
				<DataTable table={table} label="Limits" band loading={limits.isPending} />
			)}
			<ChangeDialog
				key={change === null ? "" : `${change.kind}:${change.row.key}`}
				orgId={orgId}
				change={change}
				onClose={() => {
					setChange(null);
				}}
			/>
		</Section>
	);
}
