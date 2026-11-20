import { charCount, clampChars } from "@/lib/characters";
import { createConnectQueryKey, useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { type Environment } from "@gen/loco/environment/v1/environment_pb";
import {
	createEnvironment,
	deleteEnvironment,
	listEnvironments,
	updateEnvironment,
} from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";
import { PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { nameError, slugError } from "@/lib/new-resource";
import { slug as slugOf } from "@/lib/slug";
import { LimitedAction } from "@/components/design/LimitedAction";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage, usageLabel } from "@/lib/limits";

import { resourcesPerEnvironment } from "./environments";
import { SettingsCard, SettingsSection } from "./parts";

const MAX_DESCRIPTION = 256;

function environmentColumns({
	canWrite,
	workspaceName,
	resourcesPending,
	counts,
	deleteBlocked,
	onEdit,
	onDelete,
}: {
	canWrite: boolean;
	workspaceName: string;
	resourcesPending: boolean;
	counts: ReadonlyMap<string, number>;
	deleteBlocked: (env: Environment) => string | null;
	onEdit: (env: Environment) => void;
	onDelete: (env: Environment) => void;
}): DataColumn<Environment>[] {
	return [
		{
			id: "name",
			header: "Name",
			size: 110,
			minSize: 100,
			meta: { grow: true },
			cell: ({ row: { original: env } }) => (
				<span className="flex min-w-0 flex-col">
					<CellText text={env.name} />
					{env.name !== env.slug && <CellText text={env.slug} className="font-mono text-[12px] text-fg3" />}
				</span>
			),
		},
		{
			id: "description",
			header: "Description",
			size: 300,
			minSize: 100,
			meta: { hideOnMobile: true },
			cell: ({ row: { original: env } }) =>
				(env.description ?? "") === "" ? (
					<span className="text-fg4">—</span>
				) : (
					<CellText text={env.description ?? ""} className="text-[12.5px] text-fg2" />
				),
		},
		{
			id: "resources",
			header: "Resources",
			size: 120,
			minSize: 90,
			meta: { cellClassName: "font-mono text-[12px] text-fg3" },
			cell: ({ row: { original: env } }) => (resourcesPending ? "—" : pluralize(counts.get(env.id) ?? 0, "resource")),
		},
		{
			id: "actions",
			meta: { pinEnd: true, label: "Actions" },
			cell: ({ row: { original: env } }) => {
				const blocked = deleteBlocked(env);
				const button = (
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={`Delete ${env.name}`}
						disabled={blocked !== null}
						onClick={() => {
							onDelete(env);
						}}
					>
						<Trash2Icon />
					</Button>
				);
				return (
					<span className="flex">
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label={`Edit ${env.name}`}
							disabled={!canWrite}
							title={canWrite ? undefined : `Requires Write on ${workspaceName}`}
							onClick={() => {
								onEdit(env);
							}}
						>
							<PencilIcon />
						</Button>
						{blocked === null ? (
							button
						) : (
							<Tooltip>
								<TooltipTrigger render={<span className="inline-flex" />}>{button}</TooltipTrigger>
								<TooltipContent>{blocked}</TooltipContent>
							</Tooltip>
						)}
					</span>
				);
			},
		},
	];
}

export function EnvironmentsSection({
	orgId,
	workspaceId,
	workspaceName,
	canWrite,
	canAdmin,
}: {
	orgId: string;
	workspaceId: string;
	workspaceName: string;
	canWrite: boolean;
	canAdmin: boolean;
}) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const envsQuery = useQuery(listEnvironments, { workspaceId });
	const resourcesQuery = useTanstackQuery(workspaceResourcesOptions(transport, workspaceId, "workspace"));
	const remove = useMutation(deleteEnvironment);
	const [editing, setEditing] = useState<Environment | "new" | null>(null);
	const [deleting, setDeleting] = useState<Environment | null>(null);

	const environments = envsQuery.data?.environments ?? [];
	const limits = useOrgLimits(orgId);
	const usage =
		envsQuery.data === undefined
			? null
			: limitUsage(environments.length, limits?.environmentsPerWorkspace, "environments", "workspace");
	const createBlocked = canWrite ? limitReason(usage) : `Requires Write on ${workspaceName}`;
	const counts = resourcesPerEnvironment(resourcesQuery.data?.resources ?? []);
	const envsKey = createConnectQueryKey({ schema: listEnvironments, input: { workspaceId }, cardinality: "finite" });
	const permissionNote = !canWrite
		? `Requires Write on ${workspaceName} to add or edit environments.`
		: !canAdmin
			? `Requires Admin on ${workspaceName} to delete environments.`
			: null;

	const deleteBlocked = (env: Environment): string | null => {
		if (!canAdmin) return `Requires Admin on ${workspaceName}`;
		if (resourcesQuery.isPending) return "Checking for resources";
		const n = counts.get(env.id) ?? 0;
		return n > 0 ? `${env.name} still runs ${pluralize(n, "resource")}` : null;
	};

	const table = useDataTable("environments", {
		data: environments,
		getRowId: (env) => env.id,
		columns: environmentColumns({
			canWrite,
			workspaceName,
			resourcesPending: resourcesQuery.isPending,
			counts,
			deleteBlocked,
			onEdit: setEditing,
			onDelete: setDeleting,
		}),
	});

	const confirmDelete = (env: Environment) => {
		remove.mutate(
			{ environmentId: env.id },
			{
				onSuccess: () => {
					toast.success(`Deleted ${env.name}`);
					setDeleting(null);
					void queryClient.invalidateQueries({ queryKey: envsKey });
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete environment");
				},
			},
		);
	};

	return (
		<SettingsSection
			title="Environments"
			count={usage === null ? undefined : usageLabel(usage)}
			actions={
				<LimitedAction reason={createBlocked}>
					<Button
						variant="outline"
						size="sm"
						disabled={createBlocked !== null}
						onClick={() => {
							setEditing("new");
						}}
					>
						<PlusIcon />
						New environment
					</Button>
				</LimitedAction>
			}
		>
			<SettingsCard className="pt-1">
				{envsQuery.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				) : environments.length === 0 ? (
					<div className="px-5 py-7 text-fg3">No environments yet.</div>
				) : (
					<DataTable table={table} label="Environments" />
				)}
				{permissionNote !== null && (
					<p className="m-0 border-t border-line2 px-5 py-3 text-[12.5px] text-fg3">{permissionNote}</p>
				)}
			</SettingsCard>

			<Dialog
				open={editing !== null}
				onOpenChange={(open) => {
					if (!open) setEditing(null);
				}}
			>
				{editing !== null && (
					<EnvironmentForm
						workspaceId={workspaceId}
						workspaceName={workspaceName}
						env={editing === "new" ? undefined : editing}
						others={environments.filter((e) => e !== editing)}
						onDone={() => {
							setEditing(null);
							void queryClient.invalidateQueries({ queryKey: envsKey });
						}}
						onCancel={() => {
							setEditing(null);
						}}
					/>
				)}
			</Dialog>

			<ConfirmDialog
				open={deleting !== null}
				title={`Delete ${deleting?.name ?? ""}?`}
				description={`This removes the environment from ${workspaceName}. Loco refuses while it still has deployments.`}
				confirmLabel="Delete environment"
				pendingLabel="Deleting…"
				pending={remove.isPending}
				onConfirm={() => {
					if (deleting !== null) confirmDelete(deleting);
				}}
				onClose={() => {
					setDeleting(null);
				}}
			/>
		</SettingsSection>
	);
}

function EnvironmentForm({
	workspaceId,
	workspaceName,
	env,
	others,
	onDone,
	onCancel,
}: {
	workspaceId: string;
	workspaceName: string;
	env: Environment | undefined;
	others: Environment[];
	onDone: () => void;
	onCancel: () => void;
}) {
	const create = useMutation(createEnvironment);
	const update = useMutation(updateEnvironment);
	const [rawName, setRawName] = useState(env?.name ?? "");
	const [editedSlug, setEditedSlug] = useState<string | null>(null);
	const [desc, setDesc] = useState(env?.description ?? "");
	const pending = create.isPending || update.isPending;

	const name = rawName.trim();
	const slug = env?.slug ?? editedSlug ?? slugOf(rawName);
	const nameErr = rawName === "" ? null : nameError(rawName, new Set(others.map((e) => e.name)));
	const slugErr =
		env !== undefined || (rawName === "" && editedSlug === null)
			? null
			: slugError(slug, new Set(others.map((e) => e.slug)));
	const unchanged = env === undefined ? false : name === env.name && desc.trim() === (env.description ?? "");
	const ok = name !== "" && slug !== "" && nameErr === null && slugErr === null && !unchanged && !pending;

	const submit = () => {
		if (!ok) return;
		const description = desc.trim();
		if (env !== undefined) {
			update.mutate(
				{
					environmentId: env.id,
					...(name === env.name ? {} : { name }),
					...(description === (env.description ?? "") ? {} : { description }),
				},
				{
					onSuccess: () => {
						toast.success(`Saved ${name}`);
						onDone();
					},
					onError: (err) => {
						toastConnectError(err, "Failed to save environment");
					},
				},
			);
			return;
		}
		create.mutate(
			{ workspaceId, name, slug, description: description === "" ? undefined : description },
			{
				onSuccess: () => {
					toast.success(`Created ${name}`);
					onDone();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to create environment");
				},
			},
		);
	};

	return (
		<DialogContent title={env === undefined ? `New environment in ${workspaceName}` : `Edit ${env.name}`}>
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					<Field label="Name" error={nameErr}>
						<Input
							autoFocus
							autoComplete="off"
							value={rawName}
							placeholder="Staging"
							aria-invalid={nameErr !== null}
							className="h-9 text-[13.5px]"
							onChange={(e) => {
								setRawName(e.target.value);
							}}
						/>
					</Field>
					<Field
						label="Slug"
						error={slugErr}
						hint={
							env === undefined
								? "Its key in loco.yaml, --env and links. It can't change once the environment exists."
								: "Used in loco.yaml, --env and links; it never changes"
						}
					>
						<Input
							value={slug}
							readOnly={env !== undefined}
							autoComplete="off"
							spellCheck={false}
							aria-invalid={slugErr !== null}
							className="h-9 font-mono text-[13.5px]"
							onChange={(e) => {
								setEditedSlug(e.target.value);
							}}
						/>
					</Field>
					<Field label="Description" hint={`${charCount(desc).toString()} / ${MAX_DESCRIPTION.toString()}`}>
						<Textarea
							rows={2}
							value={desc}
							className="text-[13.5px]"
							onChange={(e) => {
								setDesc(clampChars(e.target.value, MAX_DESCRIPTION));
							}}
						/>
					</Field>
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" onClick={onCancel}>
						Cancel
					</Button>
					<Button type="submit" disabled={!ok}>
						{env === undefined ? (pending ? "Creating…" : "Create") : pending ? "Saving…" : "Save"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
