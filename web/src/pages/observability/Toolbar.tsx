import { useState } from "react";
import { ChevronDownIcon, ChevronLeftIcon, ChevronRightIcon, ClockIcon, PauseIcon, PlayIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { StatusDot } from "@/components/design/StatusGlyph";
import {
	checkCustomRange,
	customRangeMessage,
	formatSpan,
	fromLocalInput,
	parseSpan,
	presetsWithin,
	resolveWindow,
	shiftWindow,
	spanLabel,
	toLocalInput,
	windowLabel,
} from "@/lib/obs-time";
import { cn } from "@/lib/utils";

import { useObs } from "./context";

export function TimePicker() {
	const { range, setRange, maxMs } = useObs();
	const [open, setOpen] = useState(false);
	const [text, setText] = useState("");
	const [from, setFrom] = useState("");
	const [to, setTo] = useState("");
	const [error, setError] = useState<string | null>(null);
	const limit = formatSpan(maxMs);

	const onOpenChange = (next: boolean) => {
		if (next) {
			const bounds = resolveWindow(range, Date.now());
			setFrom(toLocalInput(bounds.from));
			setTo(toLocalInput(bounds.to));
			setText("");
			setError(null);
		}
		setOpen(next);
	};
	const pick = (spanMs: number) => {
		setRange({ kind: "relative", spanMs });
		setOpen(false);
	};
	const applyText = () => {
		const span = parseSpan(text);
		if (span === null) {
			setError(`Try a span like 15m, 6h or ${limit}.`);
			return;
		}
		if (span > maxMs) {
			setError(`Pick a range of ${limit} or less.`);
			return;
		}
		pick(span);
	};
	const applyCustom = () => {
		const start = fromLocalInput(from);
		const end = fromLocalInput(to);
		const now = Date.now();
		const problem = checkCustomRange(start, end, now, maxMs);
		if (problem !== null) {
			setError(customRangeMessage(problem, maxMs));
			return;
		}
		setRange(end >= now ? { kind: "relative", spanMs: now - start } : { kind: "absolute", from: start, to: end });
		setOpen(false);
	};

	return (
		<div className="flex h-8 items-stretch overflow-hidden rounded-lg border border-line2 bg-raise">
			<button
				type="button"
				aria-label="Earlier"
				title="Earlier"
				onClick={() => {
					setRange(shiftWindow(range, -1, Date.now()));
				}}
				className="flex items-center px-2 text-fg2 hover:bg-panel hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset"
			>
				<ChevronLeftIcon className="size-3.5" />
			</button>
			<Popover open={open} onOpenChange={onOpenChange}>
				<PopoverTrigger className="flex items-center gap-[7px] border-x border-line2 px-2.5 text-[13px] whitespace-nowrap hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset aria-expanded:bg-panel">
					<ClockIcon className="size-3.5 text-fg3" />
					{windowLabel(range)}
					<ChevronDownIcon className="size-3.5 text-fg3" />
				</PopoverTrigger>
				<PopoverContent align="end" aria-label="Time range" className="w-[340px] gap-2.5 p-3">
					<Input
						autoFocus
						value={text}
						onChange={(e) => {
							setText(e.target.value);
							setError(null);
						}}
						onKeyDown={(e) => {
							if (e.key === "Enter") {
								e.preventDefault();
								applyText();
							}
						}}
						aria-label="Type a time range"
						placeholder={`Type a range, like 90m or ${limit}`}
						className="font-mono"
					/>
					<div role="group" aria-label="Presets" className="grid grid-cols-2 gap-0.5">
						{presetsWithin(maxMs).map((ms) => {
							const on = range.kind === "relative" && range.spanMs === ms;
							return (
								<button
									key={ms}
									type="button"
									aria-pressed={on}
									onClick={() => {
										pick(ms);
									}}
									className={cn(
										"flex h-8 items-center gap-2.5 rounded-sm px-2 text-left text-[13px] hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
										on && "bg-panel2",
									)}
								>
									<span className="w-8 font-mono text-[11.5px] text-fg3">{formatSpan(ms)}</span>
									{spanLabel(ms)}
								</button>
							);
						})}
					</div>
					<p className="m-0 text-[12px] text-fg3">Ranges up to {limit}.</p>
					<div className="h-px bg-line" />
					<span className="text-[12.5px] text-fg3">Custom range</span>
					<label className="grid grid-cols-[40px_minmax(0,1fr)] items-center gap-2 text-[12px] text-fg3">
						From
						<Input
							type="datetime-local"
							value={from}
							onChange={(e) => {
								setFrom(e.target.value);
								setError(null);
							}}
							className="h-8 min-w-0 font-mono"
						/>
					</label>
					<label className="grid grid-cols-[40px_minmax(0,1fr)] items-center gap-2 text-[12px] text-fg3">
						To
						<Input
							type="datetime-local"
							value={to}
							onChange={(e) => {
								setTo(e.target.value);
								setError(null);
							}}
							className="h-8 min-w-0 font-mono"
						/>
					</label>
					<div className="flex items-center gap-2">
						{error !== null && (
							<span role="alert" className="text-[12px] text-bad-fg">
								{error}
							</span>
						)}
						<span className="flex-1" />
						<Button size="sm" onClick={applyCustom}>
							Apply
						</Button>
					</div>
				</PopoverContent>
			</Popover>
			<button
				type="button"
				aria-label="Later"
				title="Later"
				disabled={range.kind === "relative"}
				onClick={() => {
					setRange(shiftWindow(range, 1, Date.now()));
				}}
				className="flex items-center px-2 text-fg2 hover:bg-panel hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset disabled:opacity-40 disabled:hover:bg-transparent"
			>
				<ChevronRightIcon className="size-3.5" />
			</button>
		</div>
	);
}

export function LiveButton({
	on,
	onToggle,
	disabled = false,
	pending = false,
	detail,
	title,
}: {
	on: boolean;
	onToggle: () => void;
	disabled?: boolean;
	pending?: boolean;
	detail?: string | undefined;
	title: string;
}) {
	return (
		<Button
			variant="outline"
			aria-pressed={on}
			aria-busy={pending}
			disabled={disabled || pending}
			title={title}
			onClick={onToggle}
			className={cn("h-8", on && "border-[color-mix(in_oklab,var(--ok)_40%,transparent)] text-ok-fg")}
		>
			{on ? <StatusDot pulse className="text-ok-fg" /> : <PlayIcon className="fill-current" />}
			{pending ? "Starting…" : "Live"}
			{on && detail !== undefined && <span className="font-mono text-[11.5px] opacity-85">{detail}</span>}
			{on && <PauseIcon className="fill-current" />}
		</Button>
	);
}
