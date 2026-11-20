import { logOffset } from "./trace-time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import type { Zoom } from "../flame";
import type { LogRow } from "../rows";
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
const CLUSTER_GAP = 0.006;

export function LogMarkers({ trace, zoom, logs, highlighted, onHover, onOpen }: LogMarkerProps) {
	const timePref = useTimePreferences();
	const visible = logs
		.map((row) => ({
			row,
			left: (Number(logOffset(trace, row.entry.timestamp)) / Number(trace.duration || 1n) - zoom.a) / (zoom.b - zoom.a),
		}))
		.filter(({ left }) => left >= 0 && left <= 1)
		.toSorted((a, b) => a.left - b.left);
	const clusters: { left: number; rows: LogRow[] }[] = [];
	for (const { row, left } of visible) {
		const last = clusters.at(-1);
		if (last !== undefined && left - last.left < CLUSTER_GAP) last.rows.push(row);
		else clusters.push({ left, rows: [row] });
	}
	return (
		<div aria-label="Trace log markers" className="relative h-[12px] shrink-0">
			{clusters.map(({ left, rows }) => {
				const [row] = rows;
				if (row === undefined) return null;
				const at = formatClock(row.ts, true, timePref);
				const many = rows.length > 1;
				return (
					<button
						type="button"
						key={row.key}
						aria-label={
							many ? `${String(rows.length)} logs from ${at}: ${row.message}` : `Log at ${at}: ${row.message}`
						}
						title={rows.map((r) => `${formatClock(r.ts, true, timePref)} · ${r.message}`).join("\n")}
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
							"tv-log-marker absolute top-px h-[10px] w-[3.5px] -translate-x-1/2 rounded-[1px] opacity-70 transition-transform hover:scale-125 hover:opacity-100 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
							rows.some((r) => highlighted.has(r.key)) && "scale-125 opacity-100 ring-1 ring-foreground",
						)}
						style={{ left: `${String(left * 100)}%`, background: row.color }}
					/>
				);
			})}
		</div>
	);
}
