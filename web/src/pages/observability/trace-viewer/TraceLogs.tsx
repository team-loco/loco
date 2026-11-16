import { useTraceResources } from "./TraceColors";
import { useMemo, useRef, useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { descendantIds, type Zoom } from "../flame";
import type { LogRow } from "../rows";
import type { TraceSummary } from "../traces";
import { TracePlot } from "./TracePlot";
import { TraceLogPanel } from "./TraceLogPanel";
import { SpanPanel } from "./SpanPanel";
import { useTraceLogs } from "./trace-logs";

export function TraceLogs({
	trace,
	selected,
	missing,
	select,
	label,
	zoom,
	onZoom,
	highlight,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	missing: boolean;
	select: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	zoom: Zoom;
	onZoom: (zoom: Zoom) => void;
	highlight: string | null;
}) {
	const logs = useTraceLogs(trace);
	const { children } = useTraceResources();
	const [hoverSpan, setHoverSpan] = useState<string | null>(null);
	const [hoverLog, setHoverLog] = useState<LogRow | null>(null);
	const [nearLog, setNearLog] = useState<string | null>(null);
	const [opened, setOpened] = useState<LogRow | null>(null);
	const [tab, setTab] = useState("logs");
	const [collapsed, setCollapsed] = useState(false);
	const rootRef = useRef<HTMLDivElement>(null);
	const ids = useMemo(
		() => (hoverSpan === null ? new Set<string>() : descendantIds(trace, hoverSpan, children)),
		[trace, hoverSpan, children],
	);

	const highlighted = useMemo(
		() =>
			new Set(
				logs.rows
					.filter((row) => ids.has(row.entry.spanId) || row.key === hoverLog?.key || row.key === nearLog)
					.map((row) => row.key),
			),
		[logs.rows, ids, hoverLog, nearLog],
	);
	const scroll = (key: string | undefined) => {
		if (key === undefined) return;
		const row = rootRef.current?.querySelector<HTMLElement>(`[data-log-key="${CSS.escape(key)}"]`);
		const list = row?.closest<HTMLElement>("[data-trace-log-scroll]");
		if (row === undefined || row === null || list === undefined || list === null) return;
		const rect = row.getBoundingClientRect(),
			bounds = list.getBoundingClientRect();
		if (rect.top < bounds.top) list.scrollTop += rect.top - bounds.top;
		else if (rect.bottom > bounds.bottom) list.scrollTop += rect.bottom - bounds.bottom;
	};

	const hover = (id: string | null) => {
		if (id === hoverSpan && nearLog === null) return;
		setHoverSpan(id);
		setNearLog(null);
		if (id !== null) {
			const descendants = descendantIds(trace, id, children);
			scroll(logs.rows.find((r) => descendants.has(r.entry.spanId))?.key);
		}
	};
	const atTime = (fraction: number | null) => {
		setHoverSpan(null);
		if (fraction === null) {
			setNearLog(null);
			return;
		}
		const ms = Number(trace.start) / 1_000_000 + (Number(trace.duration) / 1_000_000) * fraction;
		const nearest = logs.rows.reduce<LogRow | undefined>(
			(best, row) => (best === undefined || Math.abs(row.ts - ms) < Math.abs(best.ts - ms) ? row : best),
			undefined,
		);
		setNearLog(nearest?.key ?? null);
		if (nearest?.key !== nearLog) scroll(nearest?.key);
	};
	const open = (row: LogRow | null) => {
		setOpened(row);
		if (row === null) return;
		const span = trace.spans.find((s) => s.spanId === row.entry.spanId);
		if (span !== undefined) select(span);
		setTab("logs");
		setCollapsed(false);
	};
	return (
		<div
			ref={rootRef}
			className="flex min-w-0 flex-col gap-5 min-[1100px]:grid min-[1100px]:min-h-0 min-[1100px]:flex-1 min-[1100px]:grid-cols-[minmax(0,1fr)_400px]"
		>
			<div className="flex min-h-0 min-w-0 flex-col gap-3">
				<div className="min-w-0 shrink-0 pb-1 min-[1100px]:min-h-0 min-[1100px]:flex-1 min-[1100px]:shrink min-[1100px]:overflow-auto">
					<TracePlot
						trace={trace}
						selected={selected}
						onSelect={select}
						label={label}
						zoom={zoom}
						onZoom={onZoom}
						highlight={highlight}
						logs={logs.rows}
						highlightedLogs={highlighted}
						linkedSpan={hoverLog?.entry.spanId ?? null}
						onHoverSpan={hover}
						onHoverTime={atTime}
						onHoverLog={setHoverLog}
						onOpenLog={open}
					/>
				</div>
				<TraceLogPanel
					trace={trace}
					selected={selected}
					logs={logs}
					highlighted={highlighted}
					onHover={setHoverLog}
					opened={opened}
					onOpen={open}
					tab={tab}
					onTab={setTab}
					collapsed={collapsed}
					onCollapsed={setCollapsed}
				/>
			</div>
			<div className="min-[1100px]:min-h-0 min-[1100px]:overflow-auto">
				<SpanPanel trace={trace} selected={selected} missing={missing} select={select} label={label} />
			</div>
		</div>
	);
}
