import { useTimePreferences } from "@/lib/use-time-preferences";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { ClipboardPasteIcon, EyeIcon, EyeOffIcon, LockIcon, PlusIcon, Trash2Icon, Undo2Icon } from "lucide-react";
import { useEffect, useState } from "react";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { SoonTag } from "@/components/design/SoonTag";
import { Skeleton } from "@/components/design/Skeleton";
import { Switch } from "@/components/design/Switch";
import { Textarea } from "@/components/design/Textarea";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { parseDotEnv } from "@/lib/dotenv";
import { getErrorMessage } from "@/lib/error-handler";
import { formatStarted, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";
import { LimitedAction } from "@/components/design/LimitedAction";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";

import { SECRET_NAME, type SettingsDraft } from "./settings-draft";
import { SettingsBlock } from "./SettingsSections";
import {
	secretRows,
	setterLabel,
	variableNameProblem,
	yamlBindingHint,
	type SecretRow,
	type SecretStatus,
} from "./variables";

const STATUS_TONE: Record<SecretStatus, BadgeTone> = {
	set: "ok",
	missing: "bad",
	unused: "muted",
	new: "info",
};

const STATUS_LABEL: Record<SecretStatus, string> = {
	set: "in use",
	missing: "missing",
	unused: "not used",
	new: "new",
};

const COLUMNS = "md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)_128px]";
const ROW = cn("grid grid-cols-1 items-center gap-2 border-b border-line px-3.5 py-2.5 last:border-b-0", COLUMNS);
const MASK = "••••••••";
const FORM_COLUMNS = "md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)_auto]";

function withoutKey<T>(values: Record<string, T>, key: string): Record<string, T> {
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

function useTimedReveal() {
	const [shown, setShown] = useState(false);
	useEffect(() => {
		if (!shown) return;
		const timer = setTimeout(() => {
			setShown(false);
		}, 30_000);
		return () => {
			clearTimeout(timer);
		};
	}, [shown]);
	const toggle = () => {
		setShown((value) => !value);
	};
	return { shown, toggle };
}

function countdown(circle: SVGCircleElement | null) {
	if (circle === null || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
	const animation = circle.animate([{ strokeDashoffset: 0 }, { strokeDashoffset: 100 }], {
		duration: 30_000,
		fill: "forwards",
	});
	return () => {
		animation.cancel();
	};
}

function RevealButton({
	name,
	reveal,
	disabled = false,
}: {
	name: string;
	reveal: ReturnType<typeof useTimedReveal>;
	disabled?: boolean;
}) {
	const { shown, toggle } = reveal;
	const label = shown ? `Hide ${name}` : `Show ${name} for 30 seconds`;
	return (
		<Button
			type="button"
			variant="ghost"
			size="icon-xs"
			className="relative size-[22px]"
			aria-label={label}
			title={label}
			disabled={disabled}
			onClick={toggle}
		>
			{shown ? <EyeOffIcon /> : <EyeIcon />}
			{shown && (
				<svg
					viewBox="0 0 24 24"
					aria-hidden="true"
					className="pointer-events-none absolute -inset-[3px] size-7 -rotate-90 fill-none stroke-fg2 stroke-[1.5] in-data-[theme=neobrutal]:stroke-(--ink) in-data-[theme=neobrutal]:stroke-[2.5]"
				>
					<circle ref={countdown} cx="12" cy="12" r="10.5" pathLength="100" strokeDasharray="100" />
				</svg>
			)}
		</Button>
	);
}

function PlainVariable({
	name,
	value,
	managed,
	onRemove,
}: {
	name: string;
	value: string;
	managed: boolean;
	onRemove?: () => void;
}) {
	return (
		<div className={ROW}>
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="flex items-center gap-2">
					<span className="font-mono text-[12.5px] font-medium break-all">{name}</span>
					{onRemove !== undefined && (
						<Badge size="sm" tone={STATUS_TONE.new}>
							{STATUS_LABEL.new}
						</Badge>
					)}
				</span>
				<span className="text-[11.5px] text-fg3">
					{managed ? (
						<>
							Plain · from the <code className="font-mono">env</code> map of loco.yaml
						</>
					) : (
						"Plain"
					)}
				</span>
			</div>
			<Input aria-label={`Value of ${name}`} className="font-mono" value={value} readOnly />
			{onRemove !== undefined && (
				<div className="flex items-center justify-end">
					<Button variant="ghost" size="icon-sm" aria-label={`Remove ${name}`} onClick={onRemove}>
						<Trash2Icon />
					</Button>
				</div>
			)}
		</div>
	);
}

function SecretLine({
	row,
	members,
	hint,
	bound,
	value,
	removed,
	canWrite,
	onBound,
	onValue,
	onRemove,
}: {
	row: SecretRow;
	members: ReadonlyMap<string, string>;
	hint: string | undefined;
	bound: boolean | undefined;
	value: string;
	removed: boolean;
	canWrite: boolean;
	onBound: (bound: boolean) => void;
	onValue: (value: string) => void;
	onRemove: (removed: boolean) => void;
}) {
	const timePref = useTimePreferences();
	const reveal = useTimedReveal();
	const secret = row.secret;
	const meta =
		secret === undefined
			? "No value stored"
			: `v${secret.version.toString()} · set by ${setterLabel(secret, members)} · ${formatStarted(tsMs(secret.updatedAt), timePref)}`;
	return (
		<div className={ROW}>
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="flex items-center gap-2">
					<span className={cn("font-mono text-[12.5px] font-medium break-all", removed && "text-fg3 line-through")}>
						{row.name}
					</span>
					{row.status !== undefined &&
						hint === undefined &&
						(bound === undefined || row.status === "missing" || row.status === "new") && (
							<Badge size="sm" tone={STATUS_TONE[row.status]}>
								{STATUS_LABEL[row.status]}
							</Badge>
						)}
				</span>
				<span className="text-[11.5px] text-fg3">{removed ? "Removed when you save" : meta}</span>
				{hint !== undefined && !removed && <span className="text-[11.5px] text-warn-fg">{hint}</span>}
				{bound !== undefined && !removed && (
					<span className="flex items-center gap-1.5 pt-1 text-[11.5px] text-fg3">
						<Switch
							aria-label={`Use ${row.name} in deployments`}
							checked={bound}
							disabled={!canWrite}
							onCheckedChange={onBound}
						/>
						{bound ? "Deployments read it" : "Deployments don't read it"}
					</span>
				)}
			</div>
			<Input
				type={reveal.shown ? "text" : "password"}
				aria-label={`New value for ${row.name}`}
				className="font-mono"
				autoComplete="new-password"
				placeholder={secret === undefined ? "Enter a value" : `${MASK} · type to replace`}
				value={value}
				disabled={!canWrite || removed}
				onChange={(e) => {
					onValue(e.target.value);
				}}
			/>
			<div className="flex items-center justify-end">
				{value === "" && secret !== undefined && <SoonTag />}
				<RevealButton name={row.name} reveal={reveal} disabled={value === "" || removed} />
				<span className="flex size-7 items-center justify-center text-fg3" title="Secret">
					<LockIcon aria-label="Secret" className="size-3.5" />
				</span>
				{secret === undefined ? (
					<span className="size-7" aria-hidden />
				) : (
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
	resourceName,
	managed,
	deployed,
	workspaceId,
	environmentId,
	draft,
	canWrite,
	canReadWorkspace,
	onChange,
}: {
	resourceId: string;
	resourceName: string;
	managed: boolean;
	deployed: boolean;
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
	const [adding, setAdding] = useState(false);
	const [newName, setNewName] = useState("");
	const [newValue, setNewValue] = useState("");
	const [newSecret, setNewSecret] = useState(true);

	const staged = draft.secrets ?? {};
	const stagedEnv = draft.env ?? {};
	const bindings = draft.bindings ?? {};
	const bindable = !managed && deployed;
	const declared = new Set(envQuery.data?.secretNames ?? []);
	const removed = new Set(draft.removedSecrets ?? []);
	const members = new Map((membersRes?.members ?? []).map((m) => [m.userId, m.userName || m.userEmail]));
	const rows = secretRows(secretsQuery.data?.secrets ?? [], envQuery.data?.secretNames, Object.keys(staged));
	const plain = Object.entries(envQuery.data?.env ?? {}).toSorted(([a], [b]) => a.localeCompare(b));
	const newPlain = Object.entries(stagedEnv).toSorted(([a], [b]) => a.localeCompare(b));
	const secretKind = newSecret || !bindable;
	const newProblem =
		newName === ""
			? undefined
			: variableNameProblem(newName, secretKind, {
					secrets: new Set(rows.map((row) => row.name)),
					plain: new Set([...plain.map(([name]) => name), ...Object.keys(stagedEnv)]),
				});
	const { activeOrgId } = useOrgWorkspace();
	const limits = useOrgLimits(activeOrgId ?? "");
	const kept = new Set([...(secretsQuery.data?.secrets ?? []).map((sec) => sec.name), ...Object.keys(staged)]);
	for (const name of removed) kept.delete(name);
	const secretUsage =
		secretsQuery.isLoading || secretsQuery.error !== null
			? null
			: limitUsage(kept.size, limits?.secretsPerResource, "secrets", "service");
	const addBlocked = !secretKind || kept.has(newName) ? null : limitReason(secretUsage);
	const storedNames = new Set((secretsQuery.data?.secrets ?? []).map((sec) => sec.name));

	const bindingsWith = (name: string, bound: boolean | undefined) => {
		const next = withoutKey(bindings, name);
		return bound === undefined || bound === declared.has(name) ? next : { ...next, [name]: bound };
	};
	const stageAll = (values: Record<string, string>) => {
		const fresh = Object.keys(values).filter((name) => bindable && !storedNames.has(name) && !declared.has(name));
		onChange({
			secrets: { ...staged, ...values },
			bindings: { ...bindings, ...Object.fromEntries(fresh.map((name) => [name, true])) },
		});
	};
	const stage = (name: string, value: string) => {
		if (value !== "") {
			stageAll({ [name]: value });
			return;
		}
		const fresh = !storedNames.has(name);
		onChange({ secrets: withoutKey(staged, name), bindings: fresh ? bindingsWith(name, undefined) : bindings });
	};

	return (
		<SettingsBlock
			id="set-env"
			title={
				<>
					Variables
					<span className="font-mono text-[12.5px] font-normal text-fg3">
						{rows.length + plain.length + newPlain.length}
					</span>
				</>
			}
			hint="Secret values stay hidden. Revealing a value you type shows it for 30 seconds."
			actions={
				canWrite && (
					<div className="flex gap-2">
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
						<Button
							variant="outline"
							size="sm"
							aria-expanded={adding}
							onClick={() => {
								setAdding(true);
							}}
						>
							<PlusIcon />
							Add
						</Button>
					</div>
				)
			}
		>
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
				{!secretsQuery.isLoading && rows.length === 0 && plain.length === 0 && newPlain.length === 0 && !adding && (
					<div className="px-3.5 py-4 text-fg3">
						No variables. Add secrets here or with <code className="font-mono">loco env set</code>.
					</div>
				)}
				{rows.map((row) => (
					<SecretLine
						key={row.name}
						row={row}
						members={members}
						hint={managed && row.status === "unused" ? yamlBindingHint(resourceName, [row.name]) : undefined}
						bound={bindable && row.status !== undefined ? (bindings[row.name] ?? declared.has(row.name)) : undefined}
						value={staged[row.name] ?? ""}
						removed={removed.has(row.name)}
						canWrite={canWrite}
						onBound={(bound) => {
							onChange({ bindings: bindingsWith(row.name, bound) });
						}}
						onValue={(value) => {
							stage(row.name, value);
						}}
						onRemove={(remove) => {
							const next = new Set(removed);
							if (remove) next.add(row.name);
							else next.delete(row.name);
							onChange({
								removedSecrets: [...next],
								secrets: withoutKey(staged, row.name),
								bindings: withoutKey(bindings, row.name),
							});
						}}
					/>
				))}
				{plain.map(([name, value]) => (
					<PlainVariable key={name} name={name} value={value} managed={managed} />
				))}
				{newPlain.map(([name, value]) => (
					<PlainVariable
						key={name}
						name={name}
						value={value}
						managed={managed}
						onRemove={() => {
							onChange({ env: withoutKey(stagedEnv, name) });
						}}
					/>
				))}
				{canWrite && adding && (
					<form
						noValidate
						className={cn(
							"grid grid-cols-1 items-start gap-2 border-t border-line bg-panel px-3.5 py-2.5",
							FORM_COLUMNS,
						)}
						onSubmit={(e) => {
							e.preventDefault();
							if (newName === "" || newProblem !== undefined || newValue === "") return;
							if (secretKind) stage(newName, newValue);
							else onChange({ env: { ...stagedEnv, [newName]: newValue } });
							setNewName("");
							setNewValue("");
						}}
					>
						<div className="flex flex-col gap-1">
							<Input
								autoFocus
								aria-label="New variable name"
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
							type={secretKind ? "password" : "text"}
							aria-label="New variable value"
							className="font-mono"
							autoComplete={secretKind ? "new-password" : "off"}
							placeholder="value"
							value={newValue}
							onChange={(e) => {
								setNewValue(e.target.value);
							}}
						/>
						<div className="flex items-center gap-3">
							{bindable && (
								<span className="flex items-center gap-2">
									<Switch aria-label="Store as secret" checked={newSecret} onCheckedChange={setNewSecret} />
									<span className="text-[11.5px] text-fg3">Secret</span>
								</span>
							)}
							<LimitedAction reason={addBlocked}>
								<Button
									type="submit"
									variant="outline"
									disabled={newName === "" || newProblem !== undefined || newValue === "" || addBlocked !== null}
								>
									<PlusIcon />
									{secretKind ? "Add secret" : "Add variable"}
								</Button>
							</LimitedAction>
						</div>
					</form>
				)}
			</div>
			<ImportDialog
				open={importing}
				onOpenChange={setImporting}
				onImport={(pairs) => {
					stageAll(Object.fromEntries(pairs));
				}}
			/>
		</SettingsBlock>
	);
}
