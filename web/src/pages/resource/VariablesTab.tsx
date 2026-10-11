import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { CheckIcon, ChevronDownIcon, ClipboardPasteIcon, KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { deleteSecrets, listSecrets, setSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import type { Secret } from "@gen/loco/secret/v1/secret_pb";
import { listWorkspaceMembers } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/design/AlertDialog";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Textarea } from "@/components/design/Textarea";
import { environmentTypeLabel, useEnvironments } from "@/hooks/useEnvironment";
import { parseDotEnv } from "@/lib/dotenv";
import { toastConnectError } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";

import { formatStarted } from "./format";
import { ManagedNotice } from "./ManagedNotice";

const SECRET_NAME = /^[A-Z_][A-Z0-9_]*$/;
const MEMBERS_PAGE_SIZE = 200;
const USER_ENTITY = "user";

function nameProblem(name: string): string | undefined {
	if (name === "") return "Enter a name.";
	if (!SECRET_NAME.test(name)) return "Use capital letters, digits and underscores, not starting with a digit.";
	return undefined;
}

function setterLabel(secret: Secret, members: ReadonlyMap<string, string>): string {
	if (secret.updatedByType === USER_ENTITY) return members.get(secret.updatedById) ?? secret.updatedById.slice(0, 8);
	return `${secret.updatedByType} token`;
}

function EnvironmentPicker({
	environments,
	active,
	onSelect,
}: {
	environments: Environment[];
	active: Environment;
	onSelect: (env: Environment) => void;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger render={<Button variant="outline" size="sm" aria-label="Environment" />}>
				{active.name}
				<ChevronDownIcon />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="min-w-[200px]">
				{environments.map((e) => (
					<DropdownMenuItem
						key={e.id}
						onClick={() => {
							onSelect(e);
						}}
					>
						<span className="flex-1 truncate">{e.name}</span>
						<span className="text-sm text-fg3">{environmentTypeLabel(e.type)}</span>
						{e.id === active.id && <CheckIcon className="size-3.5" />}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function VariablesTab({
	resourceId,
	resourceName,
	workspaceId,
	partial,
	canWrite,
}: {
	resourceId: string;
	resourceName: string;
	workspaceId: string;
	partial: string | undefined;
	canWrite: boolean;
}) {
	const { environments, active, setActive, isLoading: envsLoading } = useEnvironments();
	const environmentId = active?.id ?? "";
	const queryClient = useQueryClient();
	const secretsQuery = useQuery(listSecrets, { resourceId, environmentId }, { enabled: environmentId !== "" });
	const plainQuery = useQuery(getResourceEnv, { resourceId, environmentId }, { enabled: environmentId !== "" });
	const { data: membersRes } = useQuery(listWorkspaceMembers, { workspaceId, pageSize: MEMBERS_PAGE_SIZE });
	const set = useMutation(setSecrets);
	const remove = useMutation(deleteSecrets);

	const [adding, setAdding] = useState(false);
	const [pasting, setPasting] = useState(false);
	const [newName, setNewName] = useState("");
	const [newValue, setNewValue] = useState("");
	const [pasteText, setPasteText] = useState("");
	const [replacing, setReplacing] = useState<string | null>(null);
	const [replaceValue, setReplaceValue] = useState("");
	const [deleting, setDeleting] = useState<string | null>(null);

	const members = new Map((membersRes?.members ?? []).map((m) => [m.userId, m.userName || m.userEmail]));
	const secrets = secretsQuery.data?.secrets ?? [];
	const plain = Object.entries(plainQuery.data?.env ?? {}).sort(([a], [b]) => a.localeCompare(b));
	const parsed = parseDotEnv(pasteText);
	const invalidPasted = parsed.filter(([k]) => !SECRET_NAME.test(k)).map(([k]) => k);
	const newNameProblem = newName === "" ? undefined : nameProblem(newName);
	const envName = active?.name ?? "";

	const refresh = async () => {
		const key = createConnectQueryKey({
			schema: listSecrets,
			input: { resourceId, environmentId },
			cardinality: "finite",
		});
		await queryClient.invalidateQueries({ queryKey: key });
	};

	const write = async (values: Record<string, string>, done: string): Promise<boolean> => {
		try {
			const res = await set.mutateAsync({ resourceId, environmentId, values });
			await refresh();
			toast.success(`${done} (revision ${res.revision.toString()})`);
			return true;
		} catch (err) {
			toastConnectError(err, "Failed to set secrets");
			return false;
		}
	};

	const submitNew = async () => {
		if (nameProblem(newName) !== undefined) return;
		if (await write({ [newName]: newValue }, `Set ${newName} on ${resourceName} in ${envName}`)) {
			setAdding(false);
			setNewName("");
			setNewValue("");
		}
	};

	const submitPaste = async () => {
		if (parsed.length === 0 || invalidPasted.length > 0) return;
		const count = parsed.length.toString();
		if (await write(Object.fromEntries(parsed), `Set ${count} secrets on ${resourceName} in ${envName}`)) {
			setPasting(false);
			setPasteText("");
		}
	};

	const submitReplace = async () => {
		if (replacing === null) return;
		if (await write({ [replacing]: replaceValue }, `Replaced ${replacing} on ${resourceName} in ${envName}`)) {
			setReplacing(null);
			setReplaceValue("");
		}
	};

	const confirmDelete = async () => {
		if (deleting === null) return;
		try {
			await remove.mutateAsync({ resourceId, environmentId, names: [deleting] });
			await refresh();
			toast.success(`Removed ${deleting} from ${resourceName} in ${envName}`);
		} catch (err) {
			toastConnectError(err, `Failed to remove ${deleting}`);
		}
		setDeleting(null);
	};

	if (!envsLoading && active === undefined) {
		return (
			<Section title="Variables">
				<EmptyState title="No environments yet">Create an environment before setting secrets.</EmptyState>
			</Section>
		);
	}

	const actions = canWrite ? (
		<>
			<Button
				variant="outline"
				size="sm"
				onClick={() => {
					setPasting((p) => !p);
					setAdding(false);
				}}
			>
				<ClipboardPasteIcon />
				Paste .env
			</Button>
			<Button
				size="sm"
				onClick={() => {
					setAdding((a) => !a);
					setPasting(false);
				}}
			>
				<PlusIcon />
				Add secret
			</Button>
		</>
	) : undefined;

	return (
		<div className="flex flex-col gap-5">
			{partial !== undefined && (
				<ManagedNotice partial={partial}>
					loco.yaml (<span className="break-all">{partial}</span>) declares which secrets {resourceName} reads and sets its
					plain variables. Secret values never live in the file: set them here or with <code>loco env set</code>.
				</ManagedNotice>
			)}
			<Section
				title="Secrets"
				toolbar={
					active !== undefined && (
						<EnvironmentPicker environments={environments} active={active} onSelect={setActive} />
					)
				}
				actions={actions}
			>
				{adding && (
					<form
						className="flex flex-col gap-3 border-b border-line px-4 py-3.5 sm:flex-row sm:items-start"
						onSubmit={(e) => {
							e.preventDefault();
							void submitNew();
						}}
					>
						<Field label="Name" error={newNameProblem} className="sm:w-64">
							<Input
								value={newName}
								placeholder="STRIPE_API_KEY"
								autoComplete="off"
								spellCheck={false}
								className="font-mono"
								onChange={(e) => {
									setNewName(e.target.value);
								}}
							/>
						</Field>
						<Field label="Value" className="flex-1">
							<Input
								type="password"
								value={newValue}
								autoComplete="new-password"
								className="font-mono"
								onChange={(e) => {
									setNewValue(e.target.value);
								}}
							/>
						</Field>
						<div className="flex gap-2 sm:pt-[22px]">
							<Button
								type="button"
								variant="outline"
								onClick={() => {
									setAdding(false);
								}}
							>
								Cancel
							</Button>
							<Button type="submit" disabled={newName === "" || newNameProblem !== undefined || set.isPending}>
								{set.isPending ? "Saving…" : "Save"}
							</Button>
						</div>
					</form>
				)}
				{pasting && (
					<div className="flex flex-col gap-2 border-b border-line px-4 py-3.5">
						<Textarea
							value={pasteText}
							onChange={(e) => {
								setPasteText(e.target.value);
							}}
							placeholder="KEY=value"
							rows={6}
							spellCheck={false}
							className="bg-bg2 p-2.5 font-mono text-sm leading-normal"
						/>
						<div className="flex flex-wrap items-center gap-2.5">
							<span className="flex-1 text-sm text-fg3">
								{invalidPasted.length > 0
									? `Not valid secret names: ${invalidPasted.join(", ")}`
									: `${parsed.length.toString()} names parsed. Values are sent once and never shown again.`}
							</span>
							<Button
								variant="outline"
								size="sm"
								onClick={() => {
									setPasting(false);
									setPasteText("");
								}}
							>
								Cancel
							</Button>
							<Button
								size="sm"
								disabled={parsed.length === 0 || invalidPasted.length > 0 || set.isPending}
								onClick={() => void submitPaste()}
							>
								Set {parsed.length} secrets
							</Button>
						</div>
					</div>
				)}
				{secrets.length === 0 && !secretsQuery.isLoading ? (
					<EmptyState icon={<KeyRoundIcon />} title={`No secrets in ${envName}`}>
						Secrets of {resourceName} in {envName} appear here. Values are write-only: they are never shown again
						once set.
					</EmptyState>
				) : (
					<div className="overflow-x-auto">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Name</TableHead>
									<TableHead>Version</TableHead>
									<TableHead>Set by</TableHead>
									<TableHead>Updated</TableHead>
									{canWrite && <TableHead className="w-0" />}
								</TableRow>
							</TableHeader>
							<TableBody>
								{secrets.map((s) => (
									<TableRow key={s.name}>
										<TableCell className="font-mono text-sm font-semibold">{s.name}</TableCell>
										<TableCell className="text-fg2">v{s.version}</TableCell>
										<TableCell className="text-fg2">{setterLabel(s, members)}</TableCell>
										<TableCell className="whitespace-nowrap text-fg2">{formatStarted(tsMs(s.updatedAt))}</TableCell>
										{canWrite && (
											<TableCell>
												<div className="flex justify-end gap-1.5">
													<Button
														variant="outline"
														size="sm"
														onClick={() => {
															setReplacing(s.name);
															setReplaceValue("");
														}}
													>
														Replace
													</Button>
													<Button
														variant="ghost"
														size="icon-sm"
														className="text-fg3"
														aria-label={`Remove ${s.name}`}
														onClick={() => {
															setDeleting(s.name);
														}}
													>
														<Trash2Icon />
													</Button>
												</div>
											</TableCell>
										)}
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				)}
			</Section>

			<Section title="Plain variables">
				{plain.length === 0 ? (
					<EmptyState title={`No plain variables in ${envName}`}>
						The <code className="font-mono">env</code> map of loco.yaml sets them when the file is applied.
					</EmptyState>
				) : (
					<div className="overflow-x-auto">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Name</TableHead>
									<TableHead>Value from loco.yaml</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{plain.map(([k, v]) => (
									<TableRow key={k}>
										<TableCell className="font-mono text-sm font-semibold">{k}</TableCell>
										<TableCell className="font-mono text-sm break-all text-fg2">{v}</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				)}
			</Section>

			<Dialog
				open={replacing !== null}
				onOpenChange={(open: boolean) => {
					if (!open) setReplacing(null);
				}}
			>
				<DialogContent title={`Replace ${replacing ?? ""}`}>
					<form
						onSubmit={(e) => {
							e.preventDefault();
							void submitReplace();
						}}
					>
						<DialogBody>
							<Field
								label="New value"
								hint={`The current value is never shown. Saving rolls ${resourceName} in ${envName} if the value changed.`}
							>
								<Input
									type="password"
									value={replaceValue}
									autoComplete="new-password"
									className="font-mono"
									onChange={(e) => {
										setReplaceValue(e.target.value);
									}}
								/>
							</Field>
						</DialogBody>
						<DialogFooter>
							<Button
								type="button"
								variant="outline"
								onClick={() => {
									setReplacing(null);
								}}
							>
								Cancel
							</Button>
							<Button type="submit" disabled={set.isPending}>
								{set.isPending ? "Saving…" : "Replace"}
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>

			<AlertDialog
				open={deleting !== null}
				onOpenChange={(open) => {
					if (!open) setDeleting(null);
				}}
			>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Remove {deleting}?</AlertDialogTitle>
						<AlertDialogDescription>
							{resourceName} loses {deleting} in {envName}. Loco refuses while the service&apos;s active deployment still
							declares it in loco.yaml.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction variant="destructive" disabled={remove.isPending} onClick={() => void confirmDelete()}>
							Remove
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
