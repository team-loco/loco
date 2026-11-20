import { useQuery } from "@connectrpc/connect-query";
import { ClipboardPasteIcon, PlusIcon, Trash2Icon, Undo2Icon } from "lucide-react";
import { useState } from "react";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { isEnvKey, parseDotEnv } from "@/lib/dotenv";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import type { SettingsDraft } from "./settings-draft";
import { SettingsBlock } from "./SettingsSections";
import { variableNameProblem } from "./variables";

const COLUMNS = "md:grid-cols-[minmax(160px,0.8fr)_minmax(0,1.4fr)_128px]";
const ROW = cn(
	"grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-line px-3.5 py-2.5 last:border-b-0",
	COLUMNS,
);
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
	const invalid = parsed.filter(([k]) => !isEnvKey(k)).map(([k]) => k);
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
						Paste the contents of a .env file. Each line becomes a variable; names that exist are replaced when you
						save.
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
							? `Not valid variable names: ${invalid.join(", ")}`
							: `${parsed.length.toString()} ${parsed.length === 1 ? "variable" : "variables"} found`}
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
						Add {parsed.length} {parsed.length === 1 ? "variable" : "variables"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function PlainVariable({
	name,
	value,
	managed,
	fresh = false,
	removed = false,
	onValue,
	onRemove,
}: {
	name: string;
	value: string;
	managed: boolean;
	fresh?: boolean;
	removed?: boolean;
	onValue?: ((value: string) => void) | undefined;
	onRemove?: (() => void) | undefined;
}) {
	return (
		<div className={ROW}>
			<div className="col-span-2 flex min-w-0 flex-col gap-0.5 md:col-span-1">
				<span className="flex items-center gap-2">
					<span className={cn("font-mono text-[12.5px] font-medium break-all", removed && "text-fg3 line-through")}>
						{name}
					</span>
					{fresh && (
						<Badge size="sm" tone="info">
							new
						</Badge>
					)}
				</span>
				<span className="text-[11.5px] text-fg3">
					{removed ? (
						"Removed when you save"
					) : managed ? (
						<>
							From the <code className="font-mono">env</code> map of loco.yaml
						</>
					) : null}
				</span>
			</div>
			<Input
				aria-label={`Value of ${name}`}
				className={cn("font-mono", onRemove === undefined && "col-span-2 md:col-span-1")}
				value={value}
				readOnly={onValue === undefined}
				disabled={removed}
				onChange={(e) => onValue?.(e.target.value)}
			/>
			{onRemove !== undefined && (
				<div className="flex items-center justify-end">
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={removed ? `Keep ${name}` : `Remove ${name}`}
						onClick={onRemove}
					>
						{removed ? <Undo2Icon /> : <Trash2Icon />}
					</Button>
				</div>
			)}
		</div>
	);
}

export function VariablesSection({
	resourceId,
	managed,
	deployed,
	differingRegions,
	environmentId,
	draft,
	canWrite,
	onChange,
}: {
	resourceId: string;
	managed: boolean;
	deployed: boolean;
	differingRegions: string[];
	environmentId: string;
	draft: SettingsDraft;
	canWrite: boolean;
	onChange: (patch: Partial<SettingsDraft>) => void;
}) {
	const envQuery = useQuery(getResourceEnv, { resourceId, environmentId }, { enabled: environmentId !== "" });
	const [importing, setImporting] = useState(false);
	const [adding, setAdding] = useState(false);
	const [newName, setNewName] = useState("");
	const [newValue, setNewValue] = useState("");

	const stagedEnv = draft.env ?? {};
	const removedEnv = new Set(draft.removedEnv ?? []);
	const editable = !managed && deployed && canWrite;
	const current = envQuery.data?.env ?? {};
	const plain = Object.entries(current).toSorted(([a], [b]) => a.localeCompare(b));
	const newPlain = Object.entries(stagedEnv)
		.filter(([name]) => !Object.hasOwn(current, name))
		.toSorted(([a], [b]) => a.localeCompare(b));
	const newProblem =
		newName === ""
			? undefined
			: variableNameProblem(newName, new Set([...plain.map(([name]) => name), ...Object.keys(stagedEnv)]));

	return (
		<SettingsBlock
			id="set-env"
			title={
				<>
					Variables
					<span className="font-mono text-[12.5px] font-normal text-fg3">{plain.length + newPlain.length}</span>
				</>
			}
			hint="Refer to a secret of the environment with ${{ secrets.NAME }}."
			actions={
				editable && (
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
			{differingRegions.length > 0 && (
				<p role="status" className="mb-2 text-[12.5px] text-warn-fg">
					{new Intl.ListFormat("en", { style: "long", type: "conjunction" }).format(differingRegions)} run different
					variables. This list shows one of them; saving applies it to every region.
				</p>
			)}
			<div className="overflow-hidden rounded-card border border-line bg-background">
				{envQuery.isLoading && (
					<div className="flex flex-col gap-2 p-3.5">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				)}
				{envQuery.error !== null && (
					<div role="alert" className="px-3.5 py-3 text-bad-fg">
						{getErrorMessage(envQuery.error, "Failed to load variables")}
					</div>
				)}
				{!envQuery.isLoading && plain.length === 0 && newPlain.length === 0 && !adding && (
					<div className="px-3.5 py-4 text-fg3">No variables.</div>
				)}
				{plain.map(([name, value]) => {
					const gone = removedEnv.has(name);
					return (
						<PlainVariable
							key={name}
							name={name}
							value={stagedEnv[name] ?? value}
							managed={managed}
							removed={gone}
							onValue={
								editable
									? (next) => {
											onChange({
												env: next === value ? withoutKey(stagedEnv, name) : { ...stagedEnv, [name]: next },
											});
										}
									: undefined
							}
							onRemove={
								editable
									? () => {
											const nextRemoved = new Set(removedEnv);
											if (gone) nextRemoved.delete(name);
											else nextRemoved.add(name);
											onChange({ env: withoutKey(stagedEnv, name), removedEnv: [...nextRemoved] });
										}
									: undefined
							}
						/>
					);
				})}
				{newPlain.map(([name, value]) => (
					<PlainVariable
						key={name}
						name={name}
						value={value}
						managed={managed}
						fresh
						onValue={(next) => {
							onChange({ env: { ...stagedEnv, [name]: next } });
						}}
						onRemove={() => {
							onChange({ env: withoutKey(stagedEnv, name) });
						}}
					/>
				))}
				{editable && adding && (
					<form
						noValidate
						className={cn(
							"grid grid-cols-1 items-start gap-2 border-t border-line bg-panel px-3.5 py-2.5",
							FORM_COLUMNS,
						)}
						onSubmit={(e) => {
							e.preventDefault();
							if (newName === "" || newProblem !== undefined) return;
							onChange({ env: { ...stagedEnv, [newName]: newValue } });
							setNewName("");
							setNewValue("");
						}}
					>
						<div className="flex flex-col gap-1">
							<Input
								autoFocus
								aria-label="New variable name"
								className="font-mono"
								placeholder="LOG_LEVEL"
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
							aria-label="New variable value"
							className="font-mono"
							autoComplete="off"
							placeholder="value"
							value={newValue}
							onChange={(e) => {
								setNewValue(e.target.value);
							}}
						/>
						<Button type="submit" variant="outline" disabled={newName === "" || newProblem !== undefined}>
							<PlusIcon />
							Add variable
						</Button>
					</form>
				)}
			</div>
			<ImportDialog
				open={importing}
				onOpenChange={setImporting}
				onImport={(pairs) => {
					onChange({ env: { ...stagedEnv, ...Object.fromEntries(pairs) } });
				}}
			/>
		</SettingsBlock>
	);
}
