import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { ClipboardPasteIcon, PlusIcon, Trash2Icon, Undo2Icon } from "lucide-react";
import { useState } from "react";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { parseDotEnv } from "@/lib/dotenv";
import { getErrorMessage } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { LimitedAction } from "@/components/design/LimitedAction";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";

import { formatStarted } from "./format";
import { SECRET_NAME, type SettingsDraft } from "./settings-draft";
import { SettingsBlock } from "./SettingsSections";
import { secretNameProblem, secretRows, setterLabel, type SecretRow, type SecretStatus } from "./variables";

const STATUS_TONE: Record<SecretStatus, BadgeTone> = {
	set: "ok",
	missing: "bad",
	unused: "muted",
	new: "info",
};

const STATUS_LABEL: Record<SecretStatus, string> = {
	set: "set",
	missing: "missing",
	unused: "not used",
	new: "new",
};

function withoutKey(values: Record<string, string>, key: string): Record<string, string> {
	return Object.fromEntries(Object.entries(values).filter(([k]) => k !== key));
}

function ImportDialog({
	open,
	onOpenChange,
	onImport,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onImport: (pairs: [string, string][]) => void;
}) {
	const [text, setText] = useState("");
	const parsed = parseDotEnv(text);
	const invalid = parsed.filter(([k]) => !SECRET_NAME.test(k)).map(([k]) => k);
	const ready = parsed.length > 0 && invalid.length === 0;
	return (
		<Dialog
			open={open}
			onOpenChange={(next: boolean) => {
				if (!next) setText("");
				onOpenChange(next);
			}}
		>
			<DialogContent title="Import .env">
				<DialogBody>
					<span className="text-fg2">
						Paste the contents of a .env file. Each line becomes a secret; names that exist are replaced when you save.
					</span>
					<Textarea
						aria-label=".env contents"
						value={text}
						rows={8}
						spellCheck={false}
						placeholder="DATABASE_URL=postgres://…"
						className="resize-none bg-panel font-mono text-[12.5px]"
						onChange={(e) => {
							setText(e.target.value);
						}}
					/>
					<span className={cn("font-mono text-[12px]", invalid.length > 0 ? "text-bad-fg" : "text-fg3")}>
						{invalid.length > 0
							? `Not valid secret names: ${invalid.join(", ")}`
							: `${parsed.length.toString()} ${parsed.length === 1 ? "secret" : "secrets"} found`}
					</span>
				</DialogBody>
				<DialogFooter>
					<DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
					<Button
						disabled={!ready}
						onClick={() => {
							onImport(parsed);
							setText("");
							onOpenChange(false);
						}}
					>
						Add {parsed.length} {parsed.length === 1 ? "secret" : "secrets"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function SecretLine({
	row,
	members,
	value,
	removed,
	canWrite,
	onValue,
	onRemove,
}: {
	row: SecretRow;
	members: ReadonlyMap<string, string>;
	value: string;
	removed: boolean;
	canWrite: boolean;
	onValue: (value: string) => void;
	onRemove: (removed: boolean) => void;
}) {
	const secret = row.secret;
	const meta =
		secret === undefined
			? "No value stored"
			: `v${secret.version.toString()} · set by ${setterLabel(secret, members)} · ${formatStarted(tsMs(secret.updatedAt))}`;
	return (
		<div className="grid grid-cols-1 items-center gap-2 border-b border-line px-3.5 py-2.5 last:border-b-0 md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)_auto]">
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="flex items-center gap-2">
					<span className={cn("font-mono text-[12.5px] font-medium break-all", removed && "text-fg3 line-through")}>
						{row.name}
					</span>
					{row.status !== undefined && (
						<Badge size="sm" tone={STATUS_TONE[row.status]}>
							{STATUS_LABEL[row.status]}
						</Badge>
					)}
				</span>
				<span className="text-[11.5px] text-fg3">{removed ? "Removed when you save" : meta}</span>
			</div>
			<Input
				type="password"
				aria-label={`New value for ${row.name}`}
				className="font-mono"
				autoComplete="new-password"
				placeholder={secret === undefined ? "Enter a value" : "Hidden · type to replace"}
				value={value}
				disabled={!canWrite || removed}
				onChange={(e) => {
					onValue(e.target.value);
				}}
			/>
			<div className="flex justify-end">
				{secret !== undefined && (
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={removed ? `Keep ${row.name}` : `Remove ${row.name}`}
						disabled={!canWrite}
						onClick={() => {
							onRemove(!removed);
						}}
					>
						{removed ? <Undo2Icon /> : <Trash2Icon />}
					</Button>
				)}
			</div>
		</div>
	);
}

export function VariablesSection({
	resourceId,
	workspaceId,
	environmentId,
	draft,
	canWrite,
	canReadWorkspace,
	onChange,
}: {
	resourceId: string;
	workspaceId: string;
	environmentId: string;
	draft: SettingsDraft;
	canWrite: boolean;
	canReadWorkspace: boolean;
	onChange: (patch: Partial<SettingsDraft>) => void;
}) {
	const transport = useTransport();
	const secretsQuery = useQuery(listSecrets, { resourceId, environmentId }, { enabled: environmentId !== "" });
	const envQuery = useQuery(getResourceEnv, { resourceId, environmentId }, { enabled: environmentId !== "" });
	const { data: membersRes } = useTanstackQuery({
		...workspaceMembersOptions(transport, workspaceId),
		enabled: canReadWorkspace,
	});
	const [importing, setImporting] = useState(false);
	const [newName, setNewName] = useState("");
	const [newValue, setNewValue] = useState("");

	const staged = draft.secrets ?? {};
	const removed = new Set(draft.removedSecrets ?? []);
	const members = new Map((membersRes?.members ?? []).map((m) => [m.userId, m.userName || m.userEmail]));
	const rows = secretRows(secretsQuery.data?.secrets ?? [], envQuery.data?.secretNames, Object.keys(staged));
	const plain = Object.entries(envQuery.data?.env ?? {}).toSorted(([a], [b]) => a.localeCompare(b));
	const newProblem = newName === "" ? undefined : secretNameProblem(newName);
	const { activeOrgId } = useOrgWorkspace();
	const limits = useOrgLimits(activeOrgId ?? "");
	const kept = new Set([...(secretsQuery.data?.secrets ?? []).map((sec) => sec.name), ...Object.keys(staged)]);
	for (const name of removed) kept.delete(name);
	const secretUsage =
		secretsQuery.isLoading || secretsQuery.error !== null
			? null
			: limitUsage(kept.size, limits?.secretsPerResource, "secrets", "service");
	const addBlocked = kept.has(newName) ? null : limitReason(secretUsage);

	const stage = (name: string, value: string) => {
		onChange({ secrets: value === "" ? withoutKey(staged, name) : { ...staged, [name]: value } });
	};

	return (
		<SettingsBlock
			id="set-env"
			title={
				<>
					Variables
					<span className="font-mono text-[12.5px] font-normal text-fg3">{rows.length + plain.length}</span>
				</>
			}
			hint="Secret values are never shown after they are saved."
			actions={
				canWrite && (
					<Button
						variant="outline"
						size="sm"
						onClick={() => {
							setImporting(true);
						}}
					>
						<ClipboardPasteIcon />
						Import .env
					</Button>
				)
			}
		>
			<div className="flex flex-col gap-2">
				<span className="text-[13px] font-medium">Secrets</span>
				<div className="overflow-hidden rounded-card border border-line bg-background">
					{secretsQuery.isLoading && (
						<div className="flex flex-col gap-2 p-3.5">
							<Skeleton className="h-9 w-full" />
							<Skeleton className="h-9 w-full" />
						</div>
					)}
					{secretsQuery.error !== null && (
						<div role="alert" className="px-3.5 py-3 text-bad-fg">
							{getErrorMessage(secretsQuery.error, "Failed to load secrets")}
						</div>
					)}
					{!secretsQuery.isLoading && rows.length === 0 && (
						<div className="px-3.5 py-4 text-fg3">
							No secrets. Add them here or with <code className="font-mono">loco env set</code>.
						</div>
					)}
					{rows.map((row) => (
						<SecretLine
							key={row.name}
							row={row}
							members={members}
							value={staged[row.name] ?? ""}
							removed={removed.has(row.name)}
							canWrite={canWrite}
							onValue={(value) => {
								stage(row.name, value);
							}}
							onRemove={(remove) => {
								const next = new Set(removed);
								if (remove) next.add(row.name);
								else next.delete(row.name);
								onChange({ removedSecrets: [...next], secrets: withoutKey(staged, row.name) });
							}}
						/>
					))}
					{canWrite && (
						<form
							noValidate
							className="grid grid-cols-1 items-start gap-2 border-t border-line bg-panel px-3.5 py-2.5 md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)_auto]"
							onSubmit={(e) => {
								e.preventDefault();
								if (secretNameProblem(newName) !== undefined || newValue === "") return;
								stage(newName, newValue);
								setNewName("");
								setNewValue("");
							}}
						>
							<div className="flex flex-col gap-1">
								<Input
									aria-label="New secret name"
									className="font-mono"
									placeholder="STRIPE_API_KEY"
									autoComplete="off"
									spellCheck={false}
									value={newName}
									aria-invalid={newProblem !== undefined}
									onChange={(e) => {
										setNewName(e.target.value);
									}}
								/>
								{newProblem !== undefined && <span className="text-[12px] text-bad-fg">{newProblem}</span>}
							</div>
							<Input
								type="password"
								aria-label="New secret value"
								className="font-mono"
								autoComplete="new-password"
								placeholder="value"
								value={newValue}
								onChange={(e) => {
									setNewValue(e.target.value);
								}}
							/>
							<LimitedAction usage={secretUsage} reason={addBlocked}>
								<Button
									type="submit"
									variant="outline"
									disabled={newName === "" || newProblem !== undefined || newValue === "" || addBlocked !== null}
								>
									<PlusIcon />
									Add
								</Button>
							</LimitedAction>
						</form>
					)}
				</div>
			</div>
			<div className="flex flex-col gap-2">
				<span className="text-[13px] font-medium">Plain variables</span>
				<span className="text-[12.5px] text-fg3">
					Read-only here. They come from the <code className="font-mono">env</code> map of loco.yaml.
				</span>
				{plain.length > 0 && (
					<div className="overflow-hidden rounded-card border border-line bg-background">
						{plain.map(([k, v]) => (
							<div
								key={k}
								className="grid grid-cols-1 gap-1 border-b border-line px-3.5 py-2.5 last:border-b-0 md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)]"
							>
								<span className="font-mono text-[12.5px] font-medium break-all">{k}</span>
								<span className="font-mono text-[12.5px] break-all text-fg2">{v}</span>
							</div>
						))}
					</div>
				)}
			</div>
			<ImportDialog
				open={importing}
				onOpenChange={setImporting}
				onImport={(pairs) => {
					onChange({ secrets: { ...staged, ...Object.fromEntries(pairs) } });
				}}
			/>
		</SettingsBlock>
	);
}
