import { cn } from "@/lib/utils";

import { bodyParts, fmtClock, fmtTs, jsonPreview, levelStyle } from "./format";
import type { LogRow } from "./rows";
import { Dot } from "./shared";

export function LogTable({
	rows,
	compact,
	selectedKey,
	words,
	highlightAfter,
	onSelect,
}: {
	rows: LogRow[];
	compact: boolean;
	selectedKey: string | null;
	words: string[];
	highlightAfter: number | null;
	onSelect: (key: string) => void;
}) {
	const cols = compact ? "96px 56px 110px minmax(0,1fr)" : "150px 64px 130px 80px minmax(0,1fr)";
	return (
		<>
			<div
				className="grid h-[34px] items-center gap-3 border-b border-line bg-bg2 px-4 text-sm font-semibold text-fg2"
				style={{ gridTemplateColumns: cols }}
			>
				<span>Time</span>
				<span>Level</span>
				<span>Resource</span>
				{!compact && <span>Replica</span>}
				<span>Message</span>
			</div>
			{rows.map((l) => {
				const open = l.key === selectedKey;
				const style = levelStyle(l.level);
				const fresh = highlightAfter !== null && l.ts > highlightAfter;
				return (
					<div
						key={l.key}
						className={cn(
							"border-b border-l-[3px] border-b-line transition-[background] duration-900 ease-out",
							open ? "border-l-primary bg-info-bg shadow-[inset_0_0_0_1px_var(--accent)]" : style.bar,
							!open && fresh && "bg-ok-bg",
						)}
					>
						<div
							onClick={() => {
								onSelect(l.key);
							}}
							className="grid min-h-[30px] cursor-pointer items-center gap-3 px-4 font-mono text-sm hover:bg-bg2"
							style={{ gridTemplateColumns: cols }}
						>
							<span className="whitespace-nowrap text-fg4">{compact ? fmtClock(l.ts, true) : fmtTs(l.ts, true)}</span>
							<span
								className={cn(
									"inline-flex h-[18px] w-12 items-center justify-center rounded-[3px] text-[10.5px] font-semibold tracking-[0.02em]",
									style.badge,
								)}
							>
								{l.label}
							</span>
							<span className="flex min-w-0 items-center gap-1.5 font-sans text-[12.5px]">
								<Dot color={l.color} />
								<span className="truncate">{l.resourceName}</span>
							</span>
							{!compact && (
								<span className="truncate text-fg3" title={l.pod}>
									{l.replica}
								</span>
							)}
							<span className={cn("truncate", style.body)}>
								{l.json !== null ? jsonPreview(l.json, words) : bodyParts(l.entry.body, words)}
							</span>
						</div>
					</div>
				);
			})}
		</>
	);
}
