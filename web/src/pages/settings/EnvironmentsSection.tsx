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
import { BoxIcon, LockIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import { Link } from "react-router";
import { useState, type ReactNode } from "react";
import { toast } from "@/lib/toast";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { resourcesPath, secretsPath, withEnv } from "@/lib/routes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { derivedSlugError, nameError } from "@/lib/slug";
import { LimitedAction, LimitUsageCount } from "@/components/design/LimitedAction";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";

import { resourcesPerEnvironment, shownSecrets } from "./environments";
import { SettingsCard, SettingsSection } from "./parts";
import { InlineCode, InlineCodeList, withCode } from "@/components/design/InlineCode";

const MAX_DESCRIPTION = 256;

function environmentColumns({
	orgId,
	workspaceId,
	canWrite,
	workspaceName,
	deleteBlocked,
	onEdit,
	onDelete,
}: {
	orgId: string;
	workspaceId: string;
	canWrite: boolean;
	workspaceName: string;
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
			cell: ({ row: { original: env } }) => <CellText text={env.name} className="text-[13px]" />,
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
			id: "actions",
			header: "",
			minSize: 100,
			enableResizing: false,
			meta: { align: "end", fit: true, label: "Actions" },
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
					<span className="flex items-center gap-2">
						<ManageLink to={withEnv(resourcesPath(orgId, workspaceId), env.slug)} icon={<BoxIcon />}>
							Manage resources
						</ManageLink>
						<ManageLink to={withEnv(secretsPath(orgId, workspaceId), env.slug)} icon={<LockIcon />}>
							Manage secrets
						</ManageLink>
						<span className="ml-1 flex gap-0.5">
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
					</span>
				);
			},
		},
	];
}

function ManageLink({ to, icon, children }: { to: string; icon: ReactNode; children: string }) {
	return (
		<Button variant="outline" size="sm" className="h-7 max-md:hidden" nativeButton={false} render={<Link to={to} />}>
			{icon}
			{children}
		</Button>
	);
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
	const secretsQuery = useQuery(listSecrets, { environmentId: deleting?.id ?? "" }, { enabled: deleting !== null });

	const environments = envsQuery.data?.environments ?? [];
	const limits = useOrgLimits(orgId);
	const usage =
		envsQuery.data === undefined
			? null
			: limitUsage(environments.length, limits?.environmentsPerWorkspace, "environments", "workspace");
	const createBlocked = canWrite ? limitReason(usage) : `Requires Write on ${workspaceName}`;
	const counts = resourcesPerEnvironment(resourcesQuery.data?.resources ?? []);
	const envsKey = createConnectQueryKey({ schema: listEnvironments, input: { workspaceId }, cardinality: "finite" });
	const permissionNote = !canWrite ? (
		<>
			Requires Write on <InlineCode>{workspaceName}</InlineCode> to add or edit environments.
		</>
	) : !canAdmin ? (
		<>
			Requires Admin on <InlineCode>{workspaceName}</InlineCode> to delete environments.
		</>
	) : null;

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
			orgId,
			workspaceId,
			canWrite,
			workspaceName,
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
					toast.success(withCode`Deleted ${env.name}`);
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
			count={usage === null ? undefined : <LimitUsageCount usage={usage} />}
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
			<SettingsCard>
				{envsQuery.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				) : environments.length === 0 ? (
					<div className="px-5 py-7 text-fg3">No environments yet.</div>
				) : (
					<DataTable table={table} label="Environments" band />
				)}
				{permissionNote !== null && (
					<p className="m-0 mx-3 border-t border-line2 pt-3 pb-3 text-[12.5px] text-fg3">{permissionNote}</p>
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
				title={
					<>
						Delete <InlineCode>{deleting?.name ?? ""}</InlineCode>?
					</>
				}
				description={
					<DeleteDescription
						workspaceName={workspaceName}
						secretNames={secretsQuery.isSuccess ? secretsQuery.data.secrets.map((secret) => secret.name) : null}
					/>
				}
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

/** What deleting an environment removes; secretNames is null while they are unknown. */
function DeleteDescription({ workspaceName, secretNames }: { workspaceName: string; secretNames: string[] | null }) {
	const { shown, more } = shownSecrets(secretNames ?? []);
	return (
		<>
			This removes the environment from <InlineCode>{workspaceName}</InlineCode>. Loco refuses while it still has
			deployments.
			{secretNames === null && " It also permanently deletes its secrets."}
			{secretNames !== null && secretNames.length > 0 && (
				<>
					{" "}
					It also permanently deletes its {secretNames.length.toString()}{" "}
					{secretNames.length === 1 ? "secret" : "secrets"}:{" "}
					<InlineCodeList names={shown}>{more > 0 ? `${more.toString()} more` : undefined}</InlineCodeList>.
				</>
			)}
		</>
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
	const [desc, setDesc] = useState(env?.description ?? "");
	const pending = create.isPending || update.isPending;

	const name = rawName.trim();
	const nameErr =
		rawName === ""
			? null
			: (nameError(rawName, new Set(others.map((e) => e.name))) ??
				(env === undefined ? derivedSlugError(rawName, new Set(others.map((e) => e.slug))) : null));
	const unchanged = env === undefined ? false : name === env.name && desc.trim() === (env.description ?? "");
	const ok = name !== "" && nameErr === null && !unchanged && !pending;

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
						toast.success(withCode`Saved ${name}`);
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
			{ workspaceId, name, description: description === "" ? undefined : description },
			{
				onSuccess: () => {
					toast.success(withCode`Created ${name}`);
					onDone();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to create environment");
				},
			},
		);
	};

	return (
		<DialogContent
			title={
				env === undefined ? (
					<>
						New environment in <InlineCode>{workspaceName}</InlineCode>
					</>
				) : (
					<>
						Edit <InlineCode>{env.name}</InlineCode>
					</>
				)
			}
			showCloseButton={false}
		>
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
							aria-invalid={nameErr !== null}
							className="h-9 text-[13.5px]"
							onChange={(e) => {
								setRawName(e.target.value);
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
