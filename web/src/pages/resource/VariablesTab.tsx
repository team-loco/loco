import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ClipboardPasteIcon, KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
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
import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Textarea } from "@/components/design/Textarea";
import { useEnvironments } from "@/hooks/useEnvironment";
import { parseDotEnv } from "@/lib/dotenv";
import { toastConnectError } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";

import { formatStarted } from "./format";

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

type SecretStatus = "set" | "missing" | "unused";

type SecretRow = {
	name: string;
	secret: Secret | undefined;
	status: SecretStatus | undefined;
};

const STATUS_TONE: Record<SecretStatus, BadgeTone> = {
	set: "ok",
	missing: "bad",
	unused: "muted",
};

const STATUS_LABEL: Record<SecretStatus, string> = {
	set: "set",
	missing: "missing",
	unused: "not used",
};

function rowStatus(stored: boolean, declared: ReadonlySet<string> | undefined, name: string): SecretStatus | undefined {
	if (declared === undefined) return undefined;
	if (!declared.has(name)) return "unused";
	if (stored) return "set";
	return "missing";
}

function secretRows(secrets: Secret[], declaredNames: string[] | undefined): SecretRow[] {
	const stored = new Map(secrets.map((s) => [s.name, s]));
	const declared = declaredNames === undefined ? undefined : new Set(declaredNames);
	const names = new Set([...stored.keys(), ...(declaredNames ?? [])]);
	return [...names]
		.toSorted((a, b) => a.localeCompare(b))
		.map((name) => {
			const secret = stored.get(name);
			const status = rowStatus(secret !== undefined, declared, name);
			return { name, secret, status };
		});
}

function StatusTag({ status }: { status: SecretStatus | undefined }) {
	if (status === undefined) return null;
	return (
		<Badge size="sm" tone={STATUS_TONE[status]}>
			{STATUS_LABEL[status]}
		</Badge>
	);
}

function RowActions({
	row,
	onEdit,
	onDelete,
}: {
	row: SecretRow;
	onEdit: (name: string, stored: boolean) => void;
	onDelete: (name: string) => void;
}) {
	if (row.secret === undefined) {
		return (
			<Button
				size="sm"
				onClick={() => {
					onEdit(row.name, false);
				}}
			>
				Set value
			</Button>
		);
	}
	return (
		<>
			<Button
				variant="outline"
				size="sm"
				onClick={() => {
					onEdit(row.name, true);
				}}
			>
				Replace
			</Button>
			<Button
				variant="ghost"
				size="icon-sm"
				className="text-fg3"
				aria-label={`Remove ${row.name}`}
				onClick={() => {
					onDelete(row.name);
				}}
			>
				<Trash2Icon />
			</Button>
		</>
	);
}

export function VariablesTab({
	resourceId,
	workspaceId,
	canWrite,
}: {
	resourceId: string;
	workspaceId: string;
	canWrite: boolean;
}) {
	const { active, isLoading: envsLoading } = useEnvironments();
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
	const [replacing, setReplacing] = useState<{ name: string; stored: boolean } | null>(null);
	const [replaceValue, setReplaceValue] = useState("");
	const [deleting, setDeleting] = useState<string | null>(null);

	const members = new Map((membersRes?.members ?? []).map((m) => [m.userId, m.userName || m.userEmail]));
	const secrets = secretsQuery.data?.secrets ?? [];
	const rows = secretRows(secrets, plainQuery.data?.secretNames);
	const plain = Object.entries(plainQuery.data?.env ?? {}).toSorted(([a], [b]) => a.localeCompare(b));
	const parsed = parseDotEnv(pasteText);
	const invalidPasted = parsed.filter(([k]) => !SECRET_NAME.test(k)).map(([k]) => k);
	const newNameProblem = newName === "" ? undefined : nameProblem(newName);

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
		if (await write({ [newName]: newValue }, `Set ${newName}`)) {
			setAdding(false);
			setNewName("");
			setNewValue("");
		}
	};

	const submitPaste = async () => {
		if (parsed.length === 0 || invalidPasted.length > 0) return;
		const count = parsed.length.toString();
		if (await write(Object.fromEntries(parsed), `Set ${count} secrets`)) {
			setPasting(false);
			setPasteText("");
		}
	};

	const submitReplace = async () => {
		if (replacing === null) return;
		const verb = replacing.stored ? "Replaced" : "Set";
		if (await write({ [replacing.name]: replaceValue }, `${verb} ${replacing.name}`)) {
			setReplacing(null);
			setReplaceValue("");
		}
	};

	const confirmDelete = async () => {
		if (deleting === null) return;
		try {
			await remove.mutateAsync({ resourceId, environmentId, names: [deleting] });
			await refresh();
			toast.success(`Removed ${deleting}`);
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

	const settingMissing = replacing?.stored === false;
	const replaceLabel = settingMissing ? "Set value" : "Replace";

	const edit = (name: string, stored: boolean) => {
		setReplacing({ name, stored });
		setReplaceValue("");
	};

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
			<Section title="Secrets" actions={actions}>
				{adding && (
					<form
						noValidate
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
							className="resize-none bg-bg2 p-2.5 font-mono text-sm leading-normal"
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
				{rows.length === 0 && !secretsQuery.isLoading ? (
					<EmptyState icon={<KeyRoundIcon />} title="No secrets for this resource">
						Either set them here or with <code className="font-mono">loco env set</code>.
					</EmptyState>
				) : (
					<>
						<ul className="divide-y divide-line md:hidden">
							{rows.map((row) => (
								<li key={row.name} className="flex flex-col gap-1.5 px-4 py-3">
									<div className="flex items-center gap-2">
										<span className="min-w-0 font-mono text-sm font-semibold break-all">{row.name}</span>
										<StatusTag status={row.status} />
									</div>
									<div className="text-sm text-fg3">
										{row.secret === undefined
											? "No value set"
											: `v${row.secret.version.toString()} · ${setterLabel(row.secret, members)} · ${formatStarted(tsMs(row.secret.updatedAt))}`}
									</div>
									{canWrite && (
										<div className="flex gap-1.5 pt-1">
											<RowActions row={row} onEdit={edit} onDelete={setDeleting} />
										</div>
									)}
								</li>
							))}
						</ul>
						<div className="hidden md:block">
							<Table>
								<TableHeader>
									<TableRow>
										<TableHead>Name</TableHead>
										<TableHead>Status</TableHead>
										<TableHead>Version</TableHead>
										<TableHead>Set by</TableHead>
										<TableHead>Updated</TableHead>
										{canWrite && <TableHead className="w-0" />}
									</TableRow>
								</TableHeader>
								<TableBody>
									{rows.map((row) => (
										<TableRow key={row.name}>
											<TableCell className="font-mono text-sm font-semibold">{row.name}</TableCell>
											<TableCell>
												<StatusTag status={row.status} />
											</TableCell>
											<TableCell className="text-fg2">
												{row.secret === undefined ? "—" : `v${row.secret.version.toString()}`}
											</TableCell>
											<TableCell className="text-fg2">
												{row.secret === undefined ? "—" : setterLabel(row.secret, members)}
											</TableCell>
											<TableCell className="whitespace-nowrap text-fg2">
												{row.secret === undefined ? "—" : formatStarted(tsMs(row.secret.updatedAt))}
											</TableCell>
											{canWrite && (
												<TableCell>
													<div className="flex justify-end gap-1.5">
														<RowActions row={row} onEdit={edit} onDelete={setDeleting} />
													</div>
												</TableCell>
											)}
										</TableRow>
									))}
								</TableBody>
							</Table>
						</div>
					</>
				)}
			</Section>

			<Section title="Plain variables">
				{plain.length === 0 ? (
					<EmptyState title="No plain variables">
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
				<DialogContent title={`${settingMissing ? "Set" : "Replace"} ${replacing?.name ?? ""}`}>
					<form
						noValidate
						onSubmit={(e) => {
							e.preventDefault();
							void submitReplace();
						}}
					>
						<DialogBody>
							<Field
								label={settingMissing ? "Value" : "New value"}
								hint={
									settingMissing
										? "This secret is declared but has no value. Saving restarts the resource in the selected environment."
										: "The current value is never shown. Saving restarts the resource in the selected environment if the value changed."
								}
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
								{set.isPending ? "Saving…" : replaceLabel}
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
							This removes the stored value. Loco refuses while the service&apos;s active deployment still declares it
							in loco.yaml.
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
