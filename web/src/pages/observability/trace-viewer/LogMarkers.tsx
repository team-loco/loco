import { logOffset } from "./trace-time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import type { Zoom } from "../flame";
import type { LogRow } from "../rows";
import { levelStyle } from "../format";
import type { TraceSummary } from "../traces";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";
export interface LogMarkerProps {
	trace: TraceSummary;
	zoom: Zoom;
	logs: LogRow[];
	highlighted: Set<string>;
	onHover: (row: LogRow | null) => void;
	onOpen: (row: LogRow) => void;
}
export function LogMarkers({ trace, zoom, logs, highlighted, onHover, onOpen }: LogMarkerProps) {
	const timePref = useTimePreferences();
	return (
		<div aria-label="Trace log markers" className="relative h-3 shrink-0">
			{logs.map((row) => {
				const fraction = Number(logOffset(trace, row.entry.timestamp)) / Number(trace.duration || 1n);
				const left = (fraction - zoom.a) / (zoom.b - zoom.a);
				if (left < 0 || left > 1) return null;
				return (
					<button
						type="button"
						key={row.key}
						aria-label={`Log at ${formatClock(row.ts, true, timePref)}: ${row.entry.body}`}
						title={`${formatClock(row.ts, true, timePref)} · ${row.entry.body}`}
						onMouseEnter={() => {
							onHover(row);
						}}
						onMouseLeave={() => {
							onHover(null);
						}}
						onFocus={() => {
							onHover(row);
						}}
						onBlur={() => {
							onHover(null);
						}}
						onClick={() => {
							onOpen(row);
						}}
						className={cn(
							"tv-log-marker absolute top-px h-2.5 w-[7px] -translate-x-1/2 rounded-[2px] opacity-70 transition-transform hover:scale-125 hover:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
							levelStyle(row.level).dot,
							highlighted.has(row.key) && "scale-125 opacity-100 ring-1 ring-foreground",
						)}
						style={{ left: `${String(left * 100)}%` }}
					/>
				);
			})}
		</div>
	);
}
