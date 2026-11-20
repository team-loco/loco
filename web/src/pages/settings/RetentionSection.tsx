import { createConnectQueryKey, useMutation } from "@connectrpc/connect-query";
import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getWorkspace, updateWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { RetentionDays, UpdateWorkspaceRequest, Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { PencilIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { LimitedAction } from "@/components/design/LimitedAction";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { SettingsCard, SettingsSection } from "./parts";

const SIGNALS = [
	{ key: "logDays", field: "logRetentionDays", label: "Logs" },
	{ key: "traceDays", field: "traceRetentionDays", label: "Traces" },
	{ key: "metricDays", field: "metricRetentionDays", label: "Metrics" },
] as const satisfies readonly {
	key: keyof RetentionDays;
	field: keyof UpdateWorkspaceRequest & keyof CountLimits;
	label: string;
}[];

type Signal = (typeof SIGNALS)[number];
type Drafts = Record<Signal["key"], string>;

const MONO = "font-mono text-[12.5px]";

function days(value: number | undefined): string {
	return value === undefined ? "—" : `${value.toString()} days`;
}

function retentionColumns(workspace: Workspace, limits: CountLimits | undefined): DataColumn<Signal>[] {
	return [
		{ id: "signal", header: "Signal", size: 160, minSize: 90, cell: ({ row }) => row.original.label },
		{
			id: "workspace",
			header: "Workspace",
			size: 160,
			minSize: 90,
			meta: { cellClassName: MONO },
			cell: ({ row: { original: signal } }) => {
				const value = workspace.retention?.[signal.key];
				return value === undefined ? <span className="text-fg3">Not set</span> : days(value);
			},
		},
		{
			id: "limit",
			header: "Organization limit",
			size: 180,
			minSize: 90,
			meta: { cellClassName: cn(MONO, "text-fg2") },
			cell: ({ row: { original: signal } }) =>
				limits === undefined ? <Skeleton className="h-4 w-16" /> : days(limits[signal.field]),
		},
		{
			id: "kept",
			header: "Kept for",
			size: 160,
			minSize: 90,
			meta: { cellClassName: MONO },
			cell: ({ row: { original: signal } }) => days(workspace.effectiveRetention?.[signal.key]),
		},
	];
}

export function RetentionSection({ workspace, canWrite }: { workspace: Workspace; canWrite: boolean }) {
	const limits = useOrgLimits(workspace.orgId);
	const [editing, setEditing] = useState(false);
	const editBlocked = !canWrite ? `Requires Write on ${workspace.name}` : limits === undefined ? "Loading" : null;
	const table = useDataTable("retention", {
		data: SIGNALS,
		getRowId: (signal) => signal.key,
		enableSorting: false,
		manualPagination: true,
		columns: retentionColumns(workspace, limits),
	});

	return (
		<SettingsSection
			title="Retention"
			actions={
				<LimitedAction reason={editBlocked}>
					<Button
						variant="outline"
						size="sm"
						disabled={editBlocked !== null}
						onClick={() => {
							setEditing(true);
						}}
					>
						<PencilIcon />
						Edit retention
					</Button>
				</LimitedAction>
			}
		>
			<SettingsCard className="pt-1">
				<DataTable table={table} label="Retention" empty={null} />
				<p className="m-0 border-t border-line2 px-5 py-3 text-[12.5px] text-fg3">
					A change applies to telemetry ingested after it; stored telemetry keeps the retention it was written with.
				</p>
			</SettingsCard>

			<Dialog open={editing} onOpenChange={setEditing}>
				{editing && limits !== undefined && (
					<RetentionForm
						workspace={workspace}
						limits={limits}
						onDone={() => {
							setEditing(false);
						}}
					/>
				)}
			</Dialog>
		</SettingsSection>
	);
}

function RetentionForm({
	workspace,
	limits,
	onDone,
}: {
	workspace: Workspace;
	limits: CountLimits;
	onDone: () => void;
}) {
	const queryClient = useQueryClient();
	const save = useMutation(updateWorkspace);
	const initial = Object.fromEntries(
		SIGNALS.map((s) => [s.key, workspace.retention?.[s.key]?.toString() ?? ""]),
	) as Drafts;
	const [drafts, setDrafts] = useState<Drafts>(initial);

	const errorFor = (signal: Signal): string | null => {
		const raw = drafts[signal.key].trim();
		if (raw === "") return null;
		const limit = limits[signal.field];
		const value = Number(raw);
		return Number.isInteger(value) && value >= 1 && value <= limit
			? null
			: `Enter whole days from 1 to ${limit.toString()}`;
	};
	const changed = SIGNALS.filter((s) => drafts[s.key].trim() !== initial[s.key]);
	const ok = changed.length > 0 && SIGNALS.every((s) => errorFor(s) === null) && !save.isPending;

	const submit = () => {
		if (!ok) return;
		const values: Partial<Record<Signal["field"], number>> = {};
		for (const signal of changed) {
			const raw = drafts[signal.key].trim();
			values[signal.field] = raw === "" ? 0 : Number(raw);
		}
		save.mutate(
			{ workspaceId: workspace.id, ...values },
			{
				onSuccess: () => {
					toast.success("Saved retention");
					void queryClient.invalidateQueries({
						queryKey: createConnectQueryKey({
							schema: getWorkspace,
							input: { workspaceId: workspace.id },
							cardinality: "finite",
						}),
					});
					onDone();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to save retention");
				},
			},
		);
	};

	return (
		<DialogContent title={`Retention for ${workspace.name}`}>
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					{SIGNALS.map((signal) => {
						const limit = limits[signal.field];
						const error = errorFor(signal);
						return (
							<Field
								key={signal.key}
								label={`${signal.label} (days)`}
								error={error}
								hint={`Up to ${limit.toString()}, the organization's limit. Leave empty to use the limit.`}
							>
								<Input
									inputMode="numeric"
									autoComplete="off"
									value={drafts[signal.key]}
									placeholder={limit.toString()}
									aria-invalid={error !== null}
									className="h-9 max-w-[160px] font-mono text-[13.5px]"
									onChange={(e) => {
										setDrafts({ ...drafts, [signal.key]: e.target.value });
									}}
								/>
							</Field>
						);
					})}
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" onClick={onDone}>
						Cancel
					</Button>
					<Button type="submit" disabled={!ok}>
						{save.isPending ? "Saving…" : "Save"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
