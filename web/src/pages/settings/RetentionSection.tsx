import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import {
	getWorkspaceRetention,
	setWorkspaceRetention,
} from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { RetentionDays, WorkspaceRetention } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { PencilIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { ErrorState } from "@/components/design/ErrorState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { LimitedAction } from "@/components/design/LimitedAction";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { toastConnectError } from "@/lib/error-handler";

import { SettingsCard, SettingsSection } from "./parts";

const SIGNALS = [
	{ key: "logDays", field: "log_days", label: "Logs" },
	{ key: "traceDays", field: "trace_days", label: "Traces" },
	{ key: "metricDays", field: "metric_days", label: "Metrics" },
] as const satisfies readonly { key: keyof RetentionDays; field: string; label: string }[];

type Signal = (typeof SIGNALS)[number];
type Drafts = Record<Signal["key"], string>;

function days(value: number | undefined): string {
	return value === undefined ? "—" : `${value.toString()} days`;
}

export function RetentionSection({
	workspaceId,
	workspaceName,
	canAdmin,
}: {
	workspaceId: string;
	workspaceName: string;
	canAdmin: boolean;
}) {
	const query = useQuery(getWorkspaceRetention, { workspaceId });
	const [editing, setEditing] = useState(false);
	const retention = query.data?.retention;
	const editBlocked = !canAdmin ? `Requires Admin on ${workspaceName}` : retention === undefined ? "Loading" : null;

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
				{query.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				) : retention === undefined ? (
					<ErrorState error={query.error} what="retention" rpc={getWorkspaceRetention} onRetry={query.refetch} />
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Signal</TableHead>
								<TableHead>Workspace</TableHead>
								<TableHead>Organization limit</TableHead>
								<TableHead>Kept for</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{SIGNALS.map((signal) => (
								<TableRow key={signal.key}>
									<TableCell>{signal.label}</TableCell>
									<TableCell className="font-mono text-[12.5px]">
										{retention.settings?.[signal.key] === undefined ? (
											<span className="text-fg3">Not set</span>
										) : (
											days(retention.settings[signal.key])
										)}
									</TableCell>
									<TableCell className="font-mono text-[12.5px] text-fg2">
										{days(retention.limits?.[signal.key])}
									</TableCell>
									<TableCell className="font-mono text-[12.5px]">{days(retention.effective?.[signal.key])}</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				)}
				<p className="m-0 border-t border-line2 px-5 py-3 text-[12.5px] text-fg3">
					A change applies to telemetry ingested after it; stored telemetry keeps the retention it was written with.
				</p>
			</SettingsCard>

			<Dialog open={editing} onOpenChange={setEditing}>
				{editing && retention !== undefined && (
					<RetentionForm
						workspaceId={workspaceId}
						workspaceName={workspaceName}
						retention={retention}
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
	workspaceId,
	workspaceName,
	retention,
	onDone,
}: {
	workspaceId: string;
	workspaceName: string;
	retention: WorkspaceRetention;
	onDone: () => void;
}) {
	const queryClient = useQueryClient();
	const save = useMutation(setWorkspaceRetention);
	const initial = Object.fromEntries(
		SIGNALS.map((s) => [s.key, retention.settings?.[s.key]?.toString() ?? ""]),
	) as Drafts;
	const [drafts, setDrafts] = useState<Drafts>(initial);

	const errorFor = (signal: Signal): string | null => {
		const raw = drafts[signal.key].trim();
		if (raw === "") return null;
		const limit = retention.limits?.[signal.key] ?? 0;
		const value = Number(raw);
		return Number.isInteger(value) && value >= 1 && value <= limit
			? null
			: `Enter whole days from 1 to ${limit.toString()}`;
	};
	const changed = SIGNALS.filter((s) => drafts[s.key].trim() !== initial[s.key]);
	const ok = changed.length > 0 && SIGNALS.every((s) => errorFor(s) === null) && !save.isPending;

	const submit = () => {
		if (!ok) return;
		const settings: Partial<Record<Signal["key"], number>> = {};
		for (const signal of changed) {
			const raw = drafts[signal.key].trim();
			if (raw !== "") settings[signal.key] = Number(raw);
		}
		save.mutate(
			{ workspaceId, settings, updateMask: { paths: changed.map((s) => s.field) } },
			{
				onSuccess: () => {
					toast.success("Saved retention");
					void queryClient.invalidateQueries({
						queryKey: createConnectQueryKey({
							schema: getWorkspaceRetention,
							input: { workspaceId },
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
		<DialogContent title={`Retention for ${workspaceName}`}>
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					{SIGNALS.map((signal) => {
						const limit = retention.limits?.[signal.key] ?? 0;
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
