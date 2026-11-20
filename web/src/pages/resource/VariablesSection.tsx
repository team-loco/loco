import { useQuery } from "@connectrpc/connect-query";
import { CheckIcon, CopyIcon, LinkIcon, PlusIcon, Trash2Icon, Undo2Icon, UploadIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { getResourceEnv, listResourceOutputs } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	Command,
	CommandEmpty,
	CommandGroup,
	CommandInput,
	CommandItem,
	CommandList,
} from "@/components/design/Command";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useCopy } from "@/hooks/useCopy";
import { useEnvironments } from "@/hooks/useEnvironment";
import { isEnvKey, parseDotEnv } from "@/lib/dotenv";
import { getErrorMessage } from "@/lib/error-handler";
import { secretsPath, withEnv } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { addedProblems, type AddedVariable, type SettingsDraft } from "./settings-draft";
import { SettingsBlock } from "./SettingsSections";
import { referenceKey, referenceOf, resolveReferences, type ReferenceContext, type Resolution } from "./variables";

const ROW =
	"grid grid-cols-[minmax(0,1fr)_auto] items-start gap-2 border-b border-line py-2 pr-2.5 pl-3.5 last:border-b-0 md:grid-cols-[minmax(150px,0.7fr)_minmax(0,1.5fr)_72px]";
const BOX = "overflow-hidden rounded-[10px] border border-line bg-background";
const CELL = "h-8 font-mono text-[12.5px]";
const RESOLUTION_TONE: Record<Resolution["tone"], string> = {
	ok: "text-fg3",
	secret: "text-fg2",
	bad: "text-warn",
};

function withoutKey<T>(values: Record<string, T>, key: string): Record<string, T> {
	return Object.fromEntries(Object.entries(values).filter(([k]) => k !== key));
}

let nextId = 0;
function newId(): string {
	nextId += 1;
	return `added-${nextId.toString()}`;
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
	const pairs = parseDotEnv(text).filter(([key]) => isEnvKey(key));
	const close = () => {
		setText("");
		onOpenChange(false);
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next: boolean) => {
				if (!next) close();
			}}
		>
			<DialogContent title="Import .env">
				<DialogBody>
					<Textarea
						aria-label=".env contents"
						value={text}
						rows={8}
						spellCheck={false}
						placeholder={"DATABASE_URL=postgres://…\nLOG_LEVEL=info"}
						className="resize-none bg-panel font-mono text-[12.5px]"
						onChange={(e) => {
							setText(e.target.value);
						}}
					/>
					<span className="font-mono text-[12px] text-fg3">
						{pairs.length === 0
							? "Nothing to import yet"
							: `${pairs.length.toString()} variable${pairs.length > 1 ? "s" : ""}: ${pairs
									.slice(0, 5)
									.map(([key]) => key)
									.join(", ")}${pairs.length > 5 ? "…" : ""}`}
					</span>
				</DialogBody>
				<DialogFooter>
					<DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
					<Button
						disabled={pairs.length === 0}
						onClick={() => {
							onImport(pairs);
							close();
						}}
					>
						{pairs.length === 0 ? "Import" : `Import ${pairs.length.toString()}`}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

interface PickItem {
	name: string;
	value: string;
	key: string;
	ref: string;
}

function ReferencePicker({
	groups,
	manageHref,
	environment,
	onPick,
}: {
	groups: { title: string; items: PickItem[] }[];
	manageHref: string;
	environment: string;
	onPick: (item: PickItem) => void;
}) {
	const [open, setOpen] = useState(false);
	return (
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverTrigger render={<Button variant="outline" size="sm" />}>
				<LinkIcon />
				Add reference
			</PopoverTrigger>
			<PopoverContent
				align="end"
				aria-label="Add a reference"
				className="flex max-h-[420px] w-[min(400px,calc(100vw-32px))] flex-col overflow-hidden p-0"
			>
				<Command>
					<CommandInput compact placeholder="Search secrets and resources" aria-label="Search secrets and resources" />
					<CommandList className="p-1.5">
						<CommandEmpty>Nothing matches.</CommandEmpty>
						{groups.map((group) => (
							<CommandGroup key={group.title} heading={group.title}>
								{group.items.map((item) => (
									<CommandItem
										key={item.ref}
										value={item.name}
										className="h-[30px] justify-between gap-3 text-[12.5px]"
										onSelect={() => {
											onPick(item);
											setOpen(false);
										}}
									>
										<span className="font-mono">{item.name}</span>
										<span className="min-w-0 truncate font-mono text-[11.5px] text-fg3">{item.value}</span>
									</CommandItem>
								))}
							</CommandGroup>
						))}
					</CommandList>
				</Command>
				<Link
					to={manageHref}
					className="block border-t border-line px-3.5 py-[9px] text-[12.5px] text-fg2 no-underline hover:text-foreground"
				>
					Manage secrets in {environment}
				</Link>
			</PopoverContent>
		</Popover>
	);
}

function VariableRow({
	name,
	value,
	fresh,
	removed,
	editable,
	resolution,
	problem,
	onName,
	onValue,
	onRemove,
}: {
	name: string;
	value: string;
	fresh: boolean;
	removed: boolean;
	editable: boolean;
	resolution: Resolution | null;
	problem: string | undefined;
	onName?: ((name: string) => void) | undefined;
	onValue: (value: string) => void;
	onRemove: () => void;
}) {
	const label = name === "" ? "new variable" : name;
	return (
		<div className={ROW}>
			<div className="col-span-2 flex flex-col gap-1 md:col-span-1">
				<Input
					aria-label={`Name of ${label}`}
					className={cn(CELL, "font-medium", removed && "text-fg3 line-through")}
					value={name}
					placeholder="NAME"
					spellCheck={false}
					autoComplete="off"
					readOnly={onName === undefined || !editable || removed}
					aria-invalid={problem !== undefined}
					onChange={(e) => onName?.(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))}
				/>
				{problem !== undefined && <span className="text-[12px] text-bad-fg">{problem}</span>}
			</div>
			<div className="flex min-w-0 flex-col gap-1">
				<Input
					aria-label={`Value of ${label}`}
					className={cn(CELL, (!editable || removed) && "bg-panel text-fg2")}
					value={value}
					placeholder="value or ${{ secrets.NAME }}"
					spellCheck={false}
					autoComplete="off"
					readOnly={!editable || removed}
					onChange={(e) => {
						onValue(e.target.value);
					}}
				/>
				{resolution !== null && !removed && (
					<span className={cn("pl-0.5 font-mono text-[11.5px] break-all", RESOLUTION_TONE[resolution.tone])}>
						{resolution.text}
					</span>
				)}
			</div>
			<span className="flex h-8 items-center justify-end gap-0.5">
				{fresh && (
					<Badge size="sm" tone="info">
						new
					</Badge>
				)}
				{editable && (
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={removed ? `Keep ${label}` : `Remove ${label}`}
						title={removed ? `Keep ${label}` : `Remove ${label}`}
						onClick={onRemove}
					>
						{removed ? <Undo2Icon /> : <Trash2Icon />}
					</Button>
				)}
			</span>
		</div>
	);
}

function ConnectionVariables({
	resourceSlug,
	outputs,
	usedBy,
}: {
	resourceSlug: string;
	outputs: { key: string; value: string }[];
	usedBy: string[];
}) {
	const [copied, copy] = useCopy();
	return (
		<div className="mt-3 flex flex-col gap-2">
			<div className="flex items-baseline justify-between gap-3">
				<span className="flex flex-col gap-0.5">
					<span className="text-[13.5px] font-medium">Connection variables</span>
					<span className="text-[12.5px] text-fg3">
						Other resources in this environment can use these to reach {resourceSlug}.
					</span>
				</span>
				{usedBy.length > 0 && <span className="text-[12.5px] text-fg3">Used by {usedBy.join(", ")}</span>}
			</div>
			<div className={BOX}>
				{outputs.map((output) => {
					const ref = referenceOf(resourceSlug, output.key);
					return (
						<div
							key={output.key}
							className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-b border-line py-2 pr-2.5 pl-3.5 text-[12.5px] last:border-b-0 md:grid-cols-[150px_minmax(0,1fr)_auto]"
						>
							<span className="font-mono font-medium">{output.key}</span>
							<span className="col-span-full row-start-2 font-mono break-all text-fg2 md:col-span-1 md:row-start-auto">
								{output.value}
							</span>
							<Button
								variant="ghost"
								size="sm"
								className="h-[26px] text-[11.5px] text-fg3"
								aria-label={`Copy ${ref}`}
								onClick={() => {
									copy(ref, ref);
								}}
							>
								{copied === ref ? <CheckIcon /> : <CopyIcon />}
								<span className="font-mono">{copied === ref ? "Copied" : ref}</span>
							</Button>
						</div>
					);
				})}
			</div>
		</div>
	);
}

export function VariablesSection({
	workspaceId,
	resourceId,
	resourceSlug,
	managed,
	deployed,
	differingRegions,
	environmentId,
	draft,
	canWrite,
	canReadWorkspace,
	onChange,
}: {
	workspaceId: string;
	resourceId: string;
	resourceSlug: string;
	managed: boolean;
	deployed: boolean;
	differingRegions: string[];
	environmentId: string;
	draft: SettingsDraft;
	canWrite: boolean;
	canReadWorkspace: boolean;
	onChange: (patch: Partial<SettingsDraft>) => void;
}) {
	const { activeOrgId } = useOrgWorkspace();
	const { environments } = useEnvironments();
	const environment = environments.find((env) => env.id === environmentId);
	const environmentName = environment?.name ?? "";
	const ready = environmentId !== "";
	const envQuery = useQuery(getResourceEnv, { resourceId, environmentId }, { enabled: ready });
	const secretsQuery = useQuery(listSecrets, { environmentId }, { enabled: ready && canReadWorkspace });
	const outputsQuery = useQuery(listResourceOutputs, { environmentId }, { enabled: ready && canReadWorkspace });
	const [importing, setImporting] = useState(false);

	const stagedEnv = draft.env ?? {};
	const removedEnv = new Set(draft.removedEnv ?? []);
	const added = draft.added ?? [];
	const editable = !managed && deployed && canWrite;
	const current = envQuery.data?.env ?? {};
	const existing = Object.entries(current).toSorted(([a], [b]) => a.localeCompare(b));
	const problems = addedProblems(draft, current);
	const secretNames = (secretsQuery.data?.secrets ?? []).map((secret) => secret.name);
	const resources = outputsQuery.data?.resources ?? [];
	const ctx: ReferenceContext = {
		environment: environmentName,
		secrets: new Set(secretNames),
		outputs: new Map(
			resources.map((resource) => [
				resource.resourceSlug,
				new Map(resource.outputs.map((output) => [output.key, output.value])),
			]),
		),
	};
	const me = resources.find((resource) => resource.resourceId === resourceId);
	const groups = [
		{
			title: `Secrets in ${environmentName}`,
			items: secretNames.map((name) => ({ name, value: "secret", key: name, ref: referenceOf("secrets", name) })),
		},
		...resources
			.filter((resource) => resource.resourceId !== resourceId)
			.map((resource) => ({
				title: resource.resourceSlug,
				items: resource.outputs
					.filter((output) => output.builtIn)
					.map((output) => ({
						name: `${resource.resourceSlug}.${output.key}`,
						value: output.value,
						key: referenceKey(resource.resourceSlug, output.key),
						ref: referenceOf(resource.resourceSlug, output.key),
					})),
			})),
	].filter((group) => group.items.length > 0);

	const taken = new Set([...Object.keys(current), ...added.map((row) => row.key)]);
	const addRow = (key: string, value: string) => {
		onChange({ added: [...added, { id: newId(), key, value }] });
	};
	const updateAdded = (id: string, patch: Partial<AddedVariable>) => {
		onChange({ added: added.map((row) => (row.id === id ? { ...row, ...patch } : row)) });
	};
	const importPairs = (pairs: [string, string][]) => {
		let env = { ...stagedEnv };
		let rows = [...added];
		const keep = new Set(removedEnv);
		for (const [key, value] of pairs) {
			if (Object.hasOwn(current, key)) {
				env = value === current[key] ? withoutKey(env, key) : { ...env, [key]: value };
				keep.delete(key);
			} else if (rows.some((row) => row.key === key)) {
				rows = rows.map((row) => (row.key === key ? { ...row, value } : row));
			} else rows.push({ id: newId(), key, value });
		}
		onChange({ env, added: rows, removedEnv: [...keep] });
	};
	const count = existing.length + added.length;

	return (
		<SettingsBlock
			id="set-env"
			title={
				<>
					Variables
					<span className="font-mono text-[12.5px] font-normal text-fg3">{count}</span>
				</>
			}
			actions={
				editable && (
					<div className="flex flex-wrap gap-2">
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								setImporting(true);
							}}
						>
							<UploadIcon />
							Import .env
						</Button>
						<ReferencePicker
							groups={groups}
							environment={environmentName}
							manageHref={withEnv(secretsPath(activeOrgId ?? "", workspaceId), environment?.slug ?? null)}
							onPick={(item) => {
								addRow(taken.has(item.key) ? `${item.key}_2` : item.key, item.ref);
							}}
						/>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								addRow("", "");
							}}
						>
							<PlusIcon />
							Add
						</Button>
					</div>
				)
			}
		>
			<span className="-mt-1 text-[12.5px] text-fg3">
				Plain values, visible to anyone who can read this resource. For anything sensitive, refer to a secret with{" "}
				<code className="font-mono text-[12px] text-fg2">{referenceOf("secrets", "NAME")}</code>.
			</span>
			{differingRegions.length > 0 && (
				<p role="status" className="m-0 text-[12.5px] text-warn-fg">
					{new Intl.ListFormat("en", { style: "long", type: "conjunction" }).format(differingRegions)} run different
					variables. This list shows one of them; saving applies it to every region.
				</p>
			)}
			<div className={BOX}>
				{envQuery.isLoading && (
					<div className="flex flex-col gap-2 p-3.5">
						<Skeleton className="h-8 w-full" />
						<Skeleton className="h-8 w-full" />
					</div>
				)}
				{envQuery.error !== null && (
					<div role="alert" className="px-3.5 py-3 text-bad-fg">
						{getErrorMessage(envQuery.error, "Failed to load variables")}
					</div>
				)}
				{!envQuery.isLoading && count === 0 && <div className="px-3.5 py-4 text-fg3">No variables.</div>}
				{existing.map(([name, value]) => {
					const gone = removedEnv.has(name);
					const shown = stagedEnv[name] ?? value;
					return (
						<VariableRow
							key={name}
							name={name}
							value={shown}
							fresh={false}
							removed={gone}
							editable={editable}
							resolution={resolveReferences(shown, ctx)}
							problem={undefined}
							onValue={(next) => {
								onChange({ env: next === value ? withoutKey(stagedEnv, name) : { ...stagedEnv, [name]: next } });
							}}
							onRemove={() => {
								const next = new Set(removedEnv);
								if (gone) next.delete(name);
								else next.add(name);
								onChange({ env: withoutKey(stagedEnv, name), removedEnv: [...next] });
							}}
						/>
					);
				})}
				{added.map((row) => (
					<VariableRow
						key={row.id}
						name={row.key}
						value={row.value}
						fresh
						removed={false}
						editable={editable}
						resolution={resolveReferences(row.value, ctx)}
						problem={problems.get(row.id)}
						onName={(key) => {
							updateAdded(row.id, { key });
						}}
						onValue={(value) => {
							updateAdded(row.id, { value });
						}}
						onRemove={() => {
							onChange({ added: added.filter((other) => other.id !== row.id) });
						}}
					/>
				))}
			</div>
			{me !== undefined && (
				<ConnectionVariables
					resourceSlug={resourceSlug}
					outputs={me.outputs.filter((output) => output.builtIn)}
					usedBy={me.usedBy}
				/>
			)}
			<ImportDialog open={importing} onOpenChange={setImporting} onImport={importPairs} />
		</SettingsBlock>
	);
}
