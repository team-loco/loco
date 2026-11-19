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
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Textarea } from "@/components/design/Textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { slug } from "@/lib/slug";
import { LimitedAction } from "@/components/design/LimitedAction";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage, usageLabel } from "@/lib/limits";

import { resourcesPerEnvironment } from "./environments";
import { SettingsCard, SettingsSection } from "./parts";

const MAX_DESCRIPTION = 256;
const MAX_DISPLAY_NAME = 63;

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
		return n > 0 ? `${env.displayName} still runs ${pluralize(n, "resource")}` : null;
	};

	const confirmDelete = (env: Environment) => {
		remove.mutate(
			{ environmentId: env.id },
			{
				onSuccess: () => {
					toast.success(`Deleted ${env.displayName}`);
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
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Name</TableHead>
								<TableHead className="hidden md:table-cell">Description</TableHead>
								<TableHead>Resources</TableHead>
								<TableHead className="w-20">
									<span className="sr-only">Actions</span>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{environments.map((env) => {
								const blocked = deleteBlocked(env);
								const button = (
									<Button
										variant="ghost"
										size="icon-sm"
										aria-label={`Delete ${env.displayName}`}
										disabled={blocked !== null}
										onClick={() => {
											setDeleting(env);
										}}
									>
										<Trash2Icon />
									</Button>
								);
								return (
									<TableRow key={env.id}>
										<TableCell>
											<div>{env.displayName}</div>
											{env.displayName !== env.name && <div className="font-mono text-[12px] text-fg3">{env.name}</div>}
										</TableCell>
										<TableCell className="hidden max-w-0 truncate text-[12.5px] text-fg2 md:table-cell">
											{(env.description ?? "") === "" ? <span className="text-fg4">—</span> : env.description}
										</TableCell>
										<TableCell className="font-mono text-[12px] text-fg3">
											{resourcesQuery.isPending ? "—" : pluralize(counts.get(env.id) ?? 0, "resource")}
										</TableCell>
										<TableCell className="text-right whitespace-nowrap">
											<Button
												variant="ghost"
												size="icon-sm"
												aria-label={`Edit ${env.displayName}`}
												disabled={!canWrite}
												title={canWrite ? undefined : `Requires Write on ${workspaceName}`}
												onClick={() => {
													setEditing(env);
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
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
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
						taken={environments.filter((e) => e !== editing).map((e) => e.name)}
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
				title={`Delete ${deleting?.displayName ?? ""}?`}
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
	taken,
	onDone,
	onCancel,
}: {
	workspaceId: string;
	workspaceName: string;
	env: Environment | undefined;
	taken: string[];
	onDone: () => void;
	onCancel: () => void;
}) {
	const create = useMutation(createEnvironment);
	const update = useMutation(updateEnvironment);
	const [raw, setRaw] = useState(env?.displayName ?? "");
	const [desc, setDesc] = useState(env?.description ?? "");
	const pending = create.isPending || update.isPending;

	const name = env?.name ?? slug(raw);
	const displayName = raw.trim();
	const error =
		env !== undefined
			? null
			: raw.trim() !== "" && name === ""
				? "Use letters or digits"
				: taken.includes(name)
					? `${name} already exists`
					: null;
	const hint = env === undefined && name !== "" && name !== raw ? `Saved as ${name}` : undefined;
	const unchanged =
		env === undefined ? false : displayName === env.displayName && desc.trim() === (env.description ?? "");
	const ok = (env === undefined ? name !== "" : displayName !== "") && error === null && !unchanged && !pending;

	const submit = () => {
		if (!ok) return;
		const description = desc.trim();
		if (env !== undefined) {
			update.mutate(
				{
					environmentId: env.id,
					...(displayName === env.displayName ? {} : { displayName }),
					...(description === (env.description ?? "") ? {} : { description }),
				},
				{
					onSuccess: () => {
						toast.success(`Saved ${displayName}`);
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
		<DialogContent title={env === undefined ? `New environment in ${workspaceName}` : `Edit ${env.displayName}`}>
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					{env !== undefined && (
						<Field label="Name" hint="Used in loco.yaml, --env and links; it never changes">
							<Input value={env.name} readOnly className="h-9 font-mono text-[13.5px]" />
						</Field>
					)}
					<Field label={env === undefined ? "Name" : "Display name"} error={error} hint={hint}>
						<Input
							autoFocus
							autoComplete="off"
							value={raw}
							maxLength={env === undefined ? undefined : MAX_DISPLAY_NAME}
							placeholder="staging"
							aria-invalid={error !== null}
							className={env === undefined ? "h-9 font-mono text-[13.5px]" : "h-9 text-[13.5px]"}
							onChange={(e) => {
								setRaw(e.target.value);
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
