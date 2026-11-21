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
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
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

function retentionColumns(
	workspace: Workspace,
	limits: CountLimits | undefined,
	edit: { drafts: Drafts; errorFor: (signal: Signal) => string | null; onDraft: (drafts: Drafts) => void } | null,
): DataColumn<Signal>[] {
	return [
		{
			id: "signal",
			header: "Signal",
			size: 120,
			minSize: 80,
			meta: { fit: true },
			cell: ({ row }) => row.original.label,
		},
		{
			id: "kept",
			header: "Kept for",
			size: 200,
			minSize: 140,
			meta: { grow: true },
			cell: ({ row: { original: signal } }) => {
				if (edit === null) {
					const own = workspace.retention?.[signal.key] !== undefined;
					return (
						<span className="flex items-baseline gap-2.5">
							<span className={MONO}>{days(workspace.effectiveRetention?.[signal.key])}</span>
							<span className={cn("text-[12px]", own ? "text-fg2" : "text-fg3")}>
								{own ? "set for this workspace" : "organization limit"}
							</span>
						</span>
					);
				}
				const { drafts, errorFor, onDraft } = edit;
				const error = errorFor(signal);
				return (
					<span className="flex flex-wrap items-center gap-x-2.5 gap-y-1 py-1.5">
						<InputGroup className="h-8 w-[120px]">
							<InputGroupInput
								inputMode="numeric"
								autoComplete="off"
								aria-label={`${signal.label} retention in days`}
								aria-invalid={error !== null}
								value={drafts[signal.key]}
								placeholder={limits?.[signal.field].toString()}
								className="font-mono text-[13px] md:text-[13px]"
								onChange={(e) => {
									onDraft({ ...drafts, [signal.key]: e.target.value.replace(/[^0-9]/g, "") });
								}}
							/>
							<InputGroupAddon align="inline-end">
								<InputGroupText className="font-sans text-[12px]">days</InputGroupText>
							</InputGroupAddon>
						</InputGroup>
						{drafts[signal.key] !== "" && (
							<Button
								variant="plain-link"
								className="underline decoration-line2 underline-offset-3"
								onClick={() => {
									onDraft({ ...drafts, [signal.key]: "" });
								}}
							>
								Use limit
							</Button>
						)}
						{error !== null && <span className="basis-full text-[12px] text-bad-fg">{error}</span>}
					</span>
				);
			},
		},
		{
			id: "limit",
			header: "Organization limit",
			size: 160,
			minSize: 80,
			meta: { hideOnMobile: true, fit: true, cellClassName: cn(MONO, "text-fg3") },
			cell: ({ row: { original: signal } }) =>
				limits === undefined ? <Skeleton className="h-4 w-16" /> : days(limits[signal.field]),
		},
	];
}

function initialDrafts(workspace: Workspace): Drafts {
	return Object.fromEntries(SIGNALS.map((s) => [s.key, workspace.retention?.[s.key]?.toString() ?? ""])) as Drafts;
}

export function RetentionSection({ workspace, canWrite }: { workspace: Workspace; canWrite: boolean }) {
	const limits = useOrgLimits(workspace.orgId);
	const queryClient = useQueryClient();
	const save = useMutation(updateWorkspace);
	const [drafts, setDrafts] = useState<Drafts | null>(null);
	const editBlocked = !canWrite ? `Requires Write on ${workspace.name}` : limits === undefined ? "Loading" : null;

	const errorFor = (signal: Signal): string | null => {
		const raw = drafts?.[signal.key] ?? "";
		if (raw === "" || limits === undefined) return null;
		const limit = limits[signal.field];
		const value = Number(raw);
		return value >= 1 && value <= limit ? null : `Enter whole days from 1 to ${limit.toString()}`;
	};
	const initial = initialDrafts(workspace);
	const changed = drafts === null ? [] : SIGNALS.filter((s) => drafts[s.key] !== initial[s.key]);
	const ok = changed.length > 0 && SIGNALS.every((s) => errorFor(s) === null) && !save.isPending;

	const submit = () => {
		if (!ok || drafts === null) return;
		const values: Partial<Record<Signal["field"], number>> = {};
		for (const signal of changed) {
			const raw = drafts[signal.key];
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
					setDrafts(null);
				},
				onError: (err) => {
					toastConnectError(err, "Failed to save retention");
				},
			},
		);
	};

	const table = useDataTable("retention", {
		data: SIGNALS,
		getRowId: (signal) => signal.key,
		columns: retentionColumns(workspace, limits, drafts === null ? null : { drafts, errorFor, onDraft: setDrafts }),
	});

	return (
		<SettingsSection
			title="Retention"
			actions={
				drafts === null && (
					<LimitedAction reason={editBlocked}>
						<Button
							variant="outline"
							size="sm"
							disabled={editBlocked !== null}
							onClick={() => {
								setDrafts(initial);
							}}
						>
							<PencilIcon />
							Edit retention
						</Button>
					</LimitedAction>
				)
			}
		>
			<SettingsCard className="pt-1">
				<form
					className="contents"
					onSubmit={(e) => {
						e.preventDefault();
						submit();
					}}
				>
					<DataTable table={table} label="Retention" />
					<div className="flex flex-wrap items-center justify-between gap-3 border-t border-line2 px-5 py-3 text-[12.5px] text-fg3">
						<p className="m-0">
							A change applies to telemetry ingested after it. Stored telemetry keeps the retention it was written with.
						</p>
						{drafts !== null && (
							<span className="flex gap-2">
								<Button
									type="button"
									variant="outline"
									onClick={() => {
										setDrafts(null);
									}}
								>
									Cancel
								</Button>
								<Button type="submit" disabled={!ok}>
									{save.isPending ? "Saving…" : "Save"}
								</Button>
							</span>
						)}
					</div>
				</form>
			</SettingsCard>
		</SettingsSection>
	);
}
