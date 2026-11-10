import { createConnectQueryKey, useMutation, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Trash2Icon, TrendingUpIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { deleteResource, scaleResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { getErrorMessage } from "@/lib/error-handler";

import {
	deleteText,
	fanOut,
	fanOutSummary,
	listedNames,
	replicasError,
	scaleChange,
	scaleNote,
	scaleTargets,
	type FanOutResult,
	type ScaleForm,
} from "./bulk";
import type { ResourceRow } from "./table";

const BLANK_FORM: ScaleForm = { replicas: "", cpu: "", memory: "" };
const KEEP = "Keep current";

function plural(n: number): string {
	return `${n.toString()} resource${n === 1 ? "" : "s"}`;
}

function report(verb: string, failVerb: string, result: FanOutResult<ResourceRow>) {
	for (const { item, error } of result.failed) {
		toast.error(`Couldn't ${failVerb} ${item.resource.name}`, {
			description: getErrorMessage(error, `Failed to ${failVerb} resource`),
		});
	}
	const summary = fanOutSummary(verb, result);
	if (result.failed.length === 0) toast.success(summary);
	else toast.error(summary);
}

function ScaleDialogContent({
	rows,
	environment,
	pending,
	onScale,
}: {
	rows: ResourceRow[];
	environment: Environment;
	pending: boolean;
	onScale: (targets: ResourceRow[], form: ScaleForm) => void;
}) {
	const [form, setForm] = useState(BLANK_FORM);
	const { targets, skipped } = scaleTargets(rows, environment.id);
	const replicas = replicasError(form);
	const blocked = pending || targets.length === 0 || scaleChange(form) === null;
	const set = (patch: Partial<ScaleForm>) => {
		setForm({ ...form, ...patch });
	};
	return (
		<DialogContent title={`Scale ${plural(rows.length)} in ${environment.name}`}>
			<form
				noValidate
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					if (!blocked) onScale(targets, form);
				}}
			>
				<DialogBody className="gap-4">
					<Field label="Replicas per region" error={replicas}>
						<Input
							inputMode="numeric"
							className="font-mono"
							placeholder={KEEP}
							value={form.replicas}
							aria-invalid={replicas !== undefined}
							onChange={(e) => {
								set({ replicas: e.target.value });
							}}
						/>
					</Field>
					<Field label="CPU per replica" hint="Cores, like 250m or 1">
						<Input
							className="font-mono"
							placeholder={KEEP}
							value={form.cpu}
							onChange={(e) => {
								set({ cpu: e.target.value });
							}}
						/>
					</Field>
					<Field label="Memory per replica" hint="Like 512Mi or 1Gi">
						<Input
							className="font-mono"
							placeholder={KEEP}
							value={form.memory}
							onChange={(e) => {
								set({ memory: e.target.value });
							}}
						/>
					</Field>
					<span className="text-[12.5px] text-fg3">{scaleNote(targets.length, skipped, environment.name)}</span>
				</DialogBody>
				<DialogFooter>
					<DialogClose render={<Button type="button" variant="outline" />}>Cancel</DialogClose>
					<Button type="submit" disabled={blocked}>
						{pending ? "Scaling…" : targets.length === 0 ? "Scale" : `Scale ${targets.length.toString()}`}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}

export function BulkActions({
	rows,
	environment,
	onClear,
	onDone,
}: {
	rows: ResourceRow[];
	environment: Environment | undefined;
	onClear: () => void;
	onDone: (failedIds: Set<string>) => void;
}) {
	const queryClient = useQueryClient();
	const transport = useTransport();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const { resourceLevel } = useMyScopes();
	const scale = useMutation(scaleResource);
	const del = useMutation(deleteResource);
	const [open, setOpen] = useState<"scale" | "delete" | null>(null);
	const [pending, setPending] = useState(false);

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const workspaceName = workspaces.find((w) => w.id === workspaceId)?.name ?? "";
	const allowed = (scope: Scope) =>
		rows.every((r) => resourceLevel(orgId, workspaceId, r.resource.id) >= scopeLevel(scope));
	const canScale = allowed(Scope.WRITE) && environment !== undefined;
	const canDelete = allowed(Scope.ADMIN) && workspaceName !== "";
	const names = rows.map((r) => r.resource.name);
	const listed = listedNames(names);

	const finish = async (result: FanOutResult<ResourceRow>, verb: string, failVerb: string) => {
		await Promise.all([
			queryClient.invalidateQueries({ queryKey: workspaceResourcesOptions(transport, workspaceId).queryKey }),
			queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({ schema: listDeployments, cardinality: "finite" }),
			}),
		]);
		report(verb, failVerb, result);
		onDone(new Set(result.failed.map((f) => f.item.resource.id)));
		setPending(false);
		setOpen(null);
	};

	const runScale = async (targets: ResourceRow[], form: ScaleForm) => {
		const change = scaleChange(form);
		if (environment === undefined || change === null) return;
		setPending(true);
		const result = await fanOut(
			targets,
			async (r) => await scale.mutateAsync({ resourceId: r.resource.id, environmentId: environment.id, ...change }),
		);
		await finish(result, "Scaled", "scale");
	};

	const runDelete = async () => {
		setPending(true);
		const result = await fanOut(rows, async (r) => await del.mutateAsync({ resourceId: r.resource.id }));
		await finish(result, "Deleted", "delete");
	};

	return (
		<>
			<span className="mr-1.5 text-[13.5px] font-medium">{rows.length.toString()} selected</span>
			<Button
				variant="outline"
				disabled={!canScale}
				title={canScale ? undefined : "Needs write access to every selected resource"}
				onClick={() => {
					setOpen("scale");
				}}
			>
				<TrendingUpIcon className="text-fg3" />
				Scale
			</Button>
			<Button
				variant="destructive-outline"
				disabled={!canDelete}
				title={canDelete ? undefined : "Needs admin access to every selected resource"}
				onClick={() => {
					setOpen("delete");
				}}
			>
				<Trash2Icon />
				Delete
			</Button>
			<Button variant="link" className="ml-auto" onClick={onClear}>
				Clear selection
			</Button>
			<Dialog
				open={open === "scale"}
				onOpenChange={(next: boolean) => {
					if (!next && !pending) setOpen(null);
				}}
			>
				{open === "scale" && environment !== undefined && (
					<ScaleDialogContent
						rows={rows}
						environment={environment}
						pending={pending}
						onScale={(targets, form) => void runScale(targets, form)}
					/>
				)}
			</Dialog>
			<ConfirmDeleteDialog
				open={open === "delete"}
				onOpenChange={(next) => {
					setOpen(next ? "delete" : null);
				}}
				title={names.length === 1 ? `Delete ${names[0] ?? ""}?` : `Delete ${plural(names.length)}?`}
				text={deleteText(rows)}
				target={workspaceName}
				confirmLabel={names.length === 1 ? "Delete" : `Delete ${names.length.toString()}`}
				pending={pending}
				onConfirm={() => void runDelete()}
			>
				{names.length > 1 && (
					<ul className="m-0 flex list-none flex-col gap-0.5 rounded-md bg-panel px-3 py-2 font-mono text-[12.5px]">
						{listed.shown.map((n) => (
							<li key={n}>{n}</li>
						))}
						{listed.more > 0 && <li className="text-fg3">and {listed.more.toString()} more</li>}
					</ul>
				)}
			</ConfirmDeleteDialog>
		</>
	);
}
