import { BoxIcon, ChevronDownIcon, ClockIcon, PauseIcon, PlayIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SoonTag } from "@/components/design/SoonTag";
import type { TimeRange } from "@/lib/obs";
import { cn } from "@/lib/utils";

import { RANGES, useObs } from "./context";
import { Dot } from "./shared";

export function TimeRangeMenu({ maxRange }: { maxRange: TimeRange | null }) {
	const { range, setRange } = useObs();
	const current = RANGES.find((r) => r.key === range);
	const maxIdx = maxRange === null ? RANGES.length - 1 : RANGES.findIndex((r) => r.key === maxRange);
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="outline" className="h-[38px] rounded-lg px-3 font-normal" />}
			>
				<ClockIcon className="text-fg3" />
				{current?.label ?? range}
				<ChevronDownIcon className="size-[13px] text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-auto min-w-[200px]">
				{RANGES.map((r, i) => {
					const disabled = i > maxIdx;
					return (
						<DropdownMenuItem
							key={r.key}
							disabled={disabled}
							onClick={() => {
								setRange(r.key);
							}}
							className={cn("justify-between gap-3", range === r.key && "bg-bg3")}
						>
							{r.label}
							<span className="flex items-center gap-1.5 text-sm text-fg3">
								{disabled && <SoonTag />}
								{r.key}
							</span>
						</DropdownMenuItem>
					);
				})}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function ResourceMenu() {
	const { resources, selected, setSelected } = useObs();
	const names = resources.map((r) => r.name);
	const sel = new Set(selected ?? names);
	const count = names.filter((n) => sel.has(n)).length;
	const first = names.find((n) => sel.has(n));
	const label =
		count === names.length || count === 0 ? "All resources" : count === 1 && first !== undefined ? first : `${String(count)} resources`;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger render={<Button variant="outline" className="h-[34px] px-2.5 font-normal" />}>
				<BoxIcon className="text-fg3" />
				{label}
				<ChevronDownIcon className="size-[13px] text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="w-auto min-w-[220px]">
				{resources.length === 0 && <div className="px-2.5 py-2 text-fg3">No resources</div>}
				{resources.map((r) => {
					const on = sel.has(r.name);
					return (
						<DropdownMenuCheckboxItem
							key={r.id}
							checked={on}
							onCheckedChange={() => {
								const next = on ? names.filter((n) => sel.has(n) && n !== r.name) : names.filter((n) => sel.has(n) || n === r.name);
								if (next.length === 0) return;
								setSelected(next.length === names.length ? null : next);
							}}
						>
							<Dot size={8} color={r.color} />
							<span className="flex-1">{r.name}</span>
						</DropdownMenuCheckboxItem>
					);
				})}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function LiveTailButton({
	on,
	rate,
	onToggle,
	disabled,
}: {
	on: boolean;
	rate: number;
	onToggle: () => void;
	disabled: boolean;
}) {
	return (
		<Button
			variant="outline"
			disabled={disabled}
			title={on ? "Pause live tail" : "Stream new logs as they arrive"}
			onClick={onToggle}
			className={cn(
				"h-[38px] w-[136px] justify-start gap-2 rounded-lg pr-1.5 pl-3 font-medium",
				on &&
					"border-[color-mix(in_oklab,var(--g-fg)_40%,transparent)] bg-ok-bg text-ok-fg hover:border-ok-fg hover:bg-ok-bg dark:bg-ok-bg dark:hover:bg-ok-bg",
			)}
		>
			<span className="relative flex size-3.5 items-center justify-center">
				{on ? (
					<>
						<span className="absolute size-3.5 animate-pulse rounded-full bg-ok-fg opacity-25" />
						<span className="size-[7px] rounded-full bg-ok-fg" />
					</>
				) : (
					<PlayIcon className="size-[13px] fill-current text-fg2" />
				)}
			</span>
			<span className="flex-1 text-left whitespace-nowrap">{on ? "Live" : "Live tail"}</span>
			{on && (
				<>
					<span className="text-[11.5px] font-medium tabular-nums opacity-85">{rate.toFixed(1)}/s</span>
					<span className="flex size-6 items-center justify-center rounded-sm bg-[color-mix(in_oklab,var(--g-fg)_14%,transparent)]">
						<PauseIcon className="size-3 fill-current" />
					</span>
				</>
			)}
		</Button>
	);
}
