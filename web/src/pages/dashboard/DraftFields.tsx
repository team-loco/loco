import { ClipboardPasteIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Slider } from "@/components/design/Slider";
import { Textarea } from "@/components/design/Textarea";
import { cn } from "@/lib/utils";

import { parseDotEnv, type DraftVar } from "./drafts";

export function StopSlider({
	label,
	stops,
	value,
	format,
	onChange,
}: {
	label: string;
	stops: string[];
	value: string;
	format: (v: string) => string;
	onChange: (v: string) => void;
}) {
	const idx = Math.max(0, stops.indexOf(value));
	const pct = (i: number) => (stops.length > 1 ? (i / (stops.length - 1)) * 100 : 0);
	return (
		<div className="flex flex-col gap-1">
			<div className="flex items-baseline justify-between">
				<span className="text-fg2">{label}</span>
				<span className="font-semibold">{format(value)}</span>
			</div>
			<Slider
				className="py-1"
				min={0}
				max={stops.length - 1}
				step={1}
				value={[idx]}
				aria-label={label}
				onValueChange={(v: number | readonly number[]) => {
					const next = typeof v === "number" ? v : (v[0] ?? 0);
					const stop = stops[next];
					if (stop !== undefined) onChange(stop);
				}}
			/>
			<div className="relative mx-2.5 h-3.5">
				{stops.map((s, i) => {
					const left = `${pct(i).toString()}%`;
					return (
						<button
							key={s}
							type="button"
							className={cn(
								"absolute -translate-x-1/2 text-xs whitespace-nowrap",
								i === idx ? "font-semibold text-foreground" : "text-fg3 hover:text-foreground",
							)}
							style={{ left }}
							onClick={() => {
								onChange(s);
							}}
						>
							{s}
						</button>
					);
				})}
			</div>
		</div>
	);
}

export function EnvVarsEditor({ vars, onChange }: { vars: DraftVar[]; onChange: (vars: DraftVar[]) => void }) {
	const [pasteOpen, setPasteOpen] = useState(false);
	const [pasteText, setPasteText] = useState("");
	const parsed = parseDotEnv(pasteText);
	const rows = vars.length > 0 ? vars : [{ key: "", value: "" }];

	const setRow = (i: number, patch: Partial<DraftVar>) => {
		onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
	};

	const applyPaste = () => {
		const merged = new Map<string, string>();
		for (const v of rows) if (v.key.trim() !== "") merged.set(v.key, v.value);
		for (const v of parsed) merged.set(v.key, v.value);
		const list = [...merged.entries()].map(([key, value]) => ({ key, value }));
		onChange(list.length > 0 ? list : [{ key: "", value: "" }]);
		setPasteOpen(false);
		setPasteText("");
	};

	return (
		<>
			{rows.map((v, i) => (
				<div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_28px] gap-1.5">
					<Input
						value={v.key}
						placeholder="KEY"
						aria-label="Variable name"
						className="font-mono text-sm"
						onChange={(e) => {
							setRow(i, { key: e.target.value });
						}}
					/>
					<Input
						value={v.value}
						placeholder="value"
						aria-label="Variable value"
						className="font-mono text-sm"
						onChange={(e) => {
							setRow(i, { value: e.target.value });
						}}
					/>
					<Button
						variant="ghost"
						size="icon"
						aria-label="Remove variable"
						className="h-8 w-7 text-fg3"
						onClick={() => {
							const next = rows.filter((_, j) => j !== i);
							onChange(next.length > 0 ? next : [{ key: "", value: "" }]);
						}}
					>
						<Trash2Icon />
					</Button>
				</div>
			))}
			<div className="flex gap-2">
				<Button
					variant="outline"
					size="sm"
					className="text-fg2"
					onClick={() => {
						onChange([...rows, { key: "", value: "" }]);
					}}
				>
					<PlusIcon />
					Add
				</Button>
				<Button
					variant="outline"
					size="sm"
					className={cn("text-fg2", pasteOpen && "bg-bg3")}
					aria-pressed={pasteOpen}
					onClick={() => {
						setPasteOpen(!pasteOpen);
						setPasteText("");
					}}
				>
					<ClipboardPasteIcon />
					Paste .env
				</Button>
			</div>
			{pasteOpen && (
				<>
					<Textarea
						autoFocus
						rows={5}
						value={pasteText}
						placeholder="KEY=value"
						className="resize-y bg-bg2 font-mono text-sm leading-normal"
						onChange={(e) => {
							setPasteText(e.target.value);
						}}
					/>
					<div className="flex justify-end">
						<Button variant="inverted" size="sm" disabled={parsed.length === 0} onClick={applyPaste}>
							Add {parsed.length}
						</Button>
					</div>
				</>
			)}
		</>
	);
}
