import { useMutation } from "@connectrpc/connect-query";
import { BracesIcon, ClipboardPasteIcon, EyeIcon, EyeOffIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { updateResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { Textarea } from "@/components/design/Textarea";
import { parseDotEnv } from "@/lib/dotenv";
import { toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { mergeEnv, validateEnv, type EnvPair } from "./env";
import type { Notice } from "./model";

interface DraftRow {
	id: number;
	key: string;
	value: string;
}

let nextId = 1;

function toRows(pairs: EnvPair[]): DraftRow[] {
	return pairs.map(([key, value]) => ({ id: nextId++, key, value }));
}

const ROW = "grid grid-cols-[minmax(160px,260px)_minmax(0,1fr)_32px] items-center gap-3 border-b border-line px-4";

export function VariablesTab({
	resourceId,
	resourceName,
	env,
	regionNames,
	hasDeployment,
	onNotice,
	onSaved,
}: {
	resourceId: string;
	resourceName: string;
	env: Record<string, string>;
	regionNames: string[];
	hasDeployment: boolean;
	onNotice: (notice: Notice) => void;
	onSaved: () => void;
}) {
	const current: EnvPair[] = Object.entries(env).sort(([a], [b]) => a.localeCompare(b));
	const [draft, setDraft] = useState<DraftRow[] | null>(null);
	const [revealed, setRevealed] = useState<Record<string, boolean>>({});
	const [pasteOpen, setPasteOpen] = useState(false);
	const [pasteText, setPasteText] = useState("");
	const [error, setError] = useState<string | undefined>(undefined);
	const save = useMutation(updateResourceEnv);

	const editing = draft !== null;
	const draftPairs: EnvPair[] = (draft ?? []).map((r) => [r.key.trim(), r.value]);
	const parsed = parseDotEnv(pasteText);
	const draftKeys = new Set(draftPairs.map(([k]) => k));
	const overwrites = parsed.filter(([k]) => draftKeys.has(k)).length;
	const pasteSummary =
		parsed.length > 0
			? `${parsed.length.toString()} parsed · ${overwrites.toString()} overwrite existing keys`
			: "Lines must be KEY=value";

	const startEdit = () => {
		setDraft(toRows(current));
		setError(undefined);
	};
	const cancel = () => {
		setDraft(null);
		setPasteOpen(false);
		setPasteText("");
		setError(undefined);
	};
	const update = (id: number, patch: Partial<DraftRow>) => {
		setDraft((d) => (d === null ? d : d.map((r) => (r.id === id ? { ...r, ...patch } : r))));
	};
	const applyPaste = () => {
		setDraft(toRows(mergeEnv(draftPairs, parsed)));
		setPasteOpen(false);
		setPasteText("");
	};
	const submit = () => {
		const problem = validateEnv(draftPairs);
		setError(problem);
		if (problem !== undefined) return;
		save.mutate(
			{ resourceId, env: Object.fromEntries(draftPairs) },
			{
				onSuccess: () => {
					cancel();
					onSaved();
					onNotice({
						tone: "info",
						title: `Updating variables on ${resourceName}`,
						message: `New deployment created in ${regionNames.join(", ")} with ${draftPairs.length.toString()} variables.`,
					});
				},
				onError: (err) => {
					toastConnectError(err, "Failed to update variables");
				},
			},
		);
	};

	return (
		<Section
			title="Environment variables"
			actions={
				editing ? (
					<>
						<Button variant="outline" className="h-[30px]" onClick={cancel} disabled={save.isPending}>
							Cancel
						</Button>
						<Button className="h-[30px]" onClick={submit} disabled={save.isPending}>
							{save.isPending ? "Saving…" : "Save and redeploy"}
						</Button>
					</>
				) : (
					<Button
						variant="outline"
						className="h-[30px]"
						onClick={startEdit}
						disabled={!hasDeployment}
						title={hasDeployment ? undefined : "Deploy the resource before editing variables"}
					>
						Edit
					</Button>
				)
			}
		>
			{!editing && current.length === 0 && (
				<EmptyState
					icon={<BracesIcon />}
					title="No environment variables"
					action={
						hasDeployment ? (
							<>
								<Button
									onClick={() => {
										setDraft([{ id: nextId++, key: "", value: "" }]);
										setError(undefined);
									}}
								>
									<PlusIcon />
									Add variable
								</Button>
								<Button
									variant="outline"
									onClick={() => {
										setDraft([]);
										setPasteOpen(true);
										setPasteText("");
										setError(undefined);
									}}
								>
									<ClipboardPasteIcon />
									Paste .env
								</Button>
							</>
						) : undefined
					}
				>
					{hasDeployment ? undefined : "Variables can be set once the resource has a deployment."}
				</EmptyState>
			)}
			{!editing &&
				current.map(([k, v]) => {
					const shown = revealed[k] === true;
					return (
						<div key={k} className={cn(ROW, "h-11")}>
							<span className="truncate font-mono text-sm font-semibold">{k}</span>
							<span className="truncate font-mono text-sm text-fg2">{shown ? v : "••••••••"}</span>
							<Button
								variant="ghost"
								size="icon-sm"
								className="text-fg3"
								aria-label={shown ? "Hide value" : "Reveal value"}
								title={shown ? "Hide value" : "Reveal value"}
								onClick={() => {
									setRevealed((r) => ({ ...r, [k]: !shown }));
								}}
							>
								{shown ? <EyeOffIcon className="size-[15px]" /> : <EyeIcon className="size-[15px]" />}
							</Button>
						</div>
					);
				})}
			{editing &&
				draft.map((r) => (
					<div key={r.id} className={cn(ROW, "h-11")}>
						<Input
							value={r.key}
							aria-label="Name"
							placeholder="KEY"
							onChange={(e) => {
								update(r.id, { key: e.target.value });
							}}
							className="h-[30px] font-mono text-sm"
						/>
						<Input
							value={r.value}
							aria-label="Value"
							placeholder="value"
							onChange={(e) => {
								update(r.id, { value: e.target.value });
							}}
							className="h-[30px] font-mono text-sm"
						/>
						<Button
							variant="ghost"
							size="icon-sm"
							className="text-fg3"
							aria-label="Remove variable"
							onClick={() => {
								setDraft((d) => (d === null ? d : d.filter((x) => x.id !== r.id)));
							}}
						>
							<Trash2Icon />
						</Button>
					</div>
				))}
			{editing && (
				<>
					<div className="flex flex-wrap items-center gap-2 px-4 py-2.5">
						<Button
							variant="ghost"
							size="sm"
							className="border-dashed border-line2 text-fg2"
							onClick={() => {
								setDraft((d) => [...(d ?? []), { id: nextId++, key: "", value: "" }]);
							}}
						>
							<PlusIcon />
							Add variable
						</Button>
						<Button
							variant="ghost"
							size="sm"
							className={cn("border-dashed border-line2 text-fg2", pasteOpen && "bg-bg3")}
							onClick={() => {
								setPasteOpen((o) => !o);
								setPasteText("");
							}}
						>
							<ClipboardPasteIcon />
							Paste .env
						</Button>
						{error !== undefined && <span className="text-sm text-bad-fg">{error}</span>}
					</div>
					{pasteOpen && (
						<div className="flex flex-col gap-2 px-4 pb-3.5">
							<Textarea
								value={pasteText}
								onChange={(e) => {
									setPasteText(e.target.value);
								}}
								placeholder="KEY=value"
								rows={6}
								className="bg-bg2 p-2.5 font-mono text-sm leading-normal"
							/>
							<div className="flex items-center gap-2.5">
								<span className="flex-1 text-sm text-fg3">{pasteSummary}</span>
								<Button
									variant="outline"
									size="sm"
									onClick={() => {
										setPasteOpen(false);
										setPasteText("");
									}}
								>
									Cancel
								</Button>
								<Button variant="inverted" size="sm" disabled={parsed.length === 0} onClick={applyPaste}>
									Add {parsed.length} variables
								</Button>
							</div>
						</div>
					)}
				</>
			)}
		</Section>
	);
}
