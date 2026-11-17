import type { QueryTraceBucketsResponse } from "@gen/loco/observability/v1/observability_pb";
import { useState, type PointerEvent, type ReactNode } from "react";

import { Skeleton } from "@/components/design/Skeleton";
import { TimeAxis } from "@/components/design/TimeAxis";
import { getErrorMessage } from "@/lib/error-handler";
import { formatCount } from "@/lib/format";
import { tsMs } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { cn } from "@/lib/utils";

import { bucketReadout, durationTick, errorRateLabel, niceTicks } from "./trace-overview";
import { durationLabel } from "./traces";

interface Brush {
	x0: number;
	x1: number;
}

interface Bucket {
	start: number;
	traces: number;
	errors: number;
	p50: bigint;
	p95: bigint;
}

const MIN_BRUSH = 0.01;
const Y_TICKS = 3;
const LATENCY_HEADROOM = 1.15;
const LATENCY_FLOOR_NANOS = 1e6;
const DASH = "4 3";

function fraction(event: PointerEvent<HTMLDivElement>): number {
	const box = event.currentTarget.getBoundingClientRect();
	return Math.min(1, Math.max(0, (event.clientX - box.left) / box.width));
}

const pct = (value: number) => `${String(value * 100)}%`;

export function TraceOverview({
	data,
	loading,
	error,
	widthSeconds,
	from,
	to,
	onZoom,
}: {
	data: QueryTraceBucketsResponse | undefined;
	loading: boolean;
	error: unknown;
	widthSeconds: number;
	from: number;
	to: number;
	onZoom: (from: number, to: number) => void;
}) {
	const timePref = useTimePreferences();
	const [brush, setBrush] = useState<Brush | null>(null);
	const [hover, setHover] = useState<number | null>(null);
	const buckets: Bucket[] = (data?.buckets ?? []).map((b) => ({
		start: tsMs(b.startTime),
		traces: Number(b.traceCount),
		errors: Number(b.errorCount),
		p50: b.p50DurationNanos,
		p95: b.p95DurationNanos,
	}));
	const widthMs = widthSeconds * 1000;
	const domainFrom = buckets[0]?.start ?? from;
	const domainTo = (buckets.at(-1)?.start ?? to - widthMs) + widthMs;
	const n = Math.max(1, buckets.length);
	const total = data?.total;
	const traces = Number(total?.traceCount ?? 0n);
	const errors = Number(total?.errorCount ?? 0n);
	const hovered = hover === null ? undefined : buckets[hover];

	const countMax = Math.max(1, ...buckets.map((b) => b.traces));
	const countTicks = niceTicks(countMax, Y_TICKS).filter((v) => Number.isInteger(v));
	const latencyMax =
		Math.max(LATENCY_FLOOR_NANOS, ...buckets.filter((b) => b.traces > 0).map((b) => Number(b.p95))) * LATENCY_HEADROOM;
	const latencyTicks = niceTicks(latencyMax, Y_TICKS);
	const line = (key: "p50" | "p95") =>
		buckets
			.flatMap((b, i) =>
				b.traces > 0 ? [`${String(((i + 0.5) / n) * 100)},${String(100 - (Number(b[key]) / latencyMax) * 100)}`] : [],
			)
			.join(" ");

	const pointer = {
		onPointerDown: (event: PointerEvent<HTMLDivElement>) => {
			if (event.button !== 0) return;
			event.currentTarget.setPointerCapture(event.pointerId);
			const x = fraction(event);
			setBrush({ x0: x, x1: x });
		},
		onPointerMove: (event: PointerEvent<HTMLDivElement>) => {
			const x = fraction(event);
			setHover(buckets.length === 0 ? null : Math.min(buckets.length - 1, Math.floor(x * buckets.length)));
			if (brush !== null) setBrush({ ...brush, x1: x });
		},
		onPointerUp: () => {
			if (brush === null) return;
			setBrush(null);
			if (Math.abs(brush.x1 - brush.x0) < MIN_BRUSH) return;
			const span = domainTo - domainFrom;
			onZoom(
				Math.round(domainFrom + Math.min(brush.x0, brush.x1) * span),
				Math.round(domainFrom + Math.max(brush.x0, brush.x1) * span),
			);
		},
		onPointerCancel: () => {
			setBrush(null);
		},
		onPointerLeave: () => {
			setHover(null);
		},
	};
	const overlay = (
		<>
			{hover !== null && (
				<span
					aria-hidden
					className="pointer-events-none absolute inset-y-0 border-l border-fg3"
					style={{ left: pct((hover + 0.5) / n) }}
				/>
			)}
			{brush !== null && (
				<span
					aria-hidden
					data-slot="chart-brush"
					className="pointer-events-none absolute inset-y-0 border border-info/55 bg-info/12"
					style={{ left: pct(Math.min(brush.x0, brush.x1)), width: pct(Math.abs(brush.x1 - brush.x0)) }}
				/>
			)}
		</>
	);
	const axis = <TimeAxis from={domainFrom} to={domainTo} />;
	const width = `${String(widthSeconds)}-second bucket`;

	if (error !== null) {
		return (
			<p role="alert" className="m-0 rounded-card bg-panel px-5 py-4 text-[12.5px] text-bad-fg">
				{getErrorMessage(error, "The trace overview could not load.")}
			</p>
		);
	}

	return (
		<div data-slot="trace-overview" className="relative grid grid-cols-2 gap-4 max-[900px]:grid-cols-1">
			<OverviewCard
				title="Traces"
				value={loading ? undefined : formatCount(traces)}
				sub={
					loading ? undefined : (
						<span className={cn(errors > 0 && "text-bad-fg")}>{errorRateLabel(traces, errors)}</span>
					)
				}
				legend={
					<>
						<Swatch className="h-2 w-2 rounded-[2px] bg-lv-info">OK</Swatch>
						<Swatch className="h-2 w-2 rounded-[2px] bg-err">Error</Swatch>
					</>
				}
				ticks={countTicks.map((v) => ({ y: 1 - v / countMax, label: formatCount(v) }))}
				label={`Traces per ${width}, ${formatCount(traces)} in range with ${formatCount(errors)} errors. Drag across the chart to zoom into a time range.`}
				loading={loading}
				pointer={pointer}
				axis={axis}
			>
				{buckets.map((b, i) => (
					<span
						key={b.start}
						aria-hidden
						className="absolute inset-y-0 flex flex-col justify-end px-px"
						style={{ left: pct(i / n), width: pct(1 / n) }}
					>
						<i
							className={cn(
								"block rounded-[1px] bg-err transition-[height] duration-300 motion-reduce:transition-none",
								b.errors > 0 ? "mb-px" : "hidden",
							)}
							style={{ height: pct(b.errors / countMax) }}
						/>
						<i
							className={cn(
								"block rounded-[1px] bg-lv-info transition-[height] duration-300 motion-reduce:transition-none",
								b.traces === b.errors && "hidden",
							)}
							style={{ height: pct((b.traces - b.errors) / countMax) }}
						/>
					</span>
				))}
				{overlay}
			</OverviewCard>
			<OverviewCard
				title="Latency"
				value={loading ? undefined : total !== undefined && traces > 0 ? durationLabel(total.p95DurationNanos) : "—"}
				sub="p95"
				legend={
					<>
						<Swatch className="h-0 w-3 border-t-2 border-info">p50</Swatch>
						<Swatch className="h-0 w-3 border-t-2 border-dashed border-warn">p95</Swatch>
					</>
				}
				ticks={latencyTicks.map((v) => ({ y: 1 - v / latencyMax, label: durationTick(v) }))}
				label={`p50 and p95 trace duration per ${width}. Drag across the chart to zoom into a time range.`}
				loading={loading}
				pointer={pointer}
				axis={axis}
			>
				<svg
					aria-hidden
					viewBox="0 0 100 100"
					preserveAspectRatio="none"
					className="absolute inset-0 size-full overflow-visible"
				>
					<polyline
						points={line("p50")}
						fill="none"
						strokeWidth={1.75}
						strokeLinejoin="round"
						vectorEffect="non-scaling-stroke"
						className="stroke-info"
					/>
					<polyline
						points={line("p95")}
						fill="none"
						strokeWidth={1.75}
						strokeLinejoin="round"
						strokeDasharray={DASH}
						vectorEffect="non-scaling-stroke"
						className="stroke-warn"
					/>
				</svg>
				{overlay}
			</OverviewCard>
			<span
				aria-live="polite"
				className={cn(
					"pointer-events-none absolute -top-[30px] left-1/2 -translate-x-1/2 rounded-[5px] bg-foreground px-2 py-[3px] font-mono text-[11.5px] whitespace-nowrap text-background opacity-0 transition-opacity duration-100",
					hovered !== undefined && "opacity-100",
				)}
			>
				{hovered !== undefined && bucketReadout({ ...hovered, widthSeconds }, timePref)}
			</span>
		</div>
	);
}

function Swatch({ className, children }: { className: string; children: ReactNode }) {
	return (
		<span className="inline-flex items-center gap-[5px] self-center text-[11.5px] text-fg3">
			<span aria-hidden className="inline-flex h-2 items-center">
				<i data-slot="chart-swatch" className={className} />
			</span>
			{children}
		</span>
	);
}

function OverviewCard({
	title,
	value,
	sub,
	legend,
	ticks,
	label,
	loading,
	pointer,
	axis,
	children,
}: {
	title: string;
	value: string | undefined;
	sub: ReactNode;
	legend: ReactNode;
	ticks: { y: number; label: string }[];
	label: string;
	loading: boolean;
	pointer: Record<string, (event: PointerEvent<HTMLDivElement>) => void>;
	axis: ReactNode;
	children: ReactNode;
}) {
	return (
		<section aria-label={title} className="flex min-w-0 flex-col gap-2.5 rounded-card bg-panel px-[18px] pt-3.5 pb-2.5">
			<div className="flex min-w-0 flex-wrap items-baseline gap-2">
				<span className="text-[13px] text-fg2">{title}</span>
				{value === undefined ? (
					<Skeleton className="h-3 w-16 self-center" />
				) : (
					<>
						<span className="font-mono text-[15px] text-foreground">{value}</span>
						<span className="font-mono text-[11.5px] text-fg3">{sub}</span>
					</>
				)}
				<span className="flex-1" />
				{legend}
			</div>
			<div className="grid grid-cols-[44px_minmax(0,1fr)] grid-rows-[120px_auto] gap-x-2 gap-y-1.5">
				<div aria-hidden className="relative font-mono text-[11px] text-fg3">
					{ticks.map((tick) => (
						<span key={tick.label} className="absolute right-0 -translate-y-1/2" style={{ top: pct(tick.y) }}>
							{tick.label}
						</span>
					))}
				</div>
				<div
					role="img"
					aria-label={label}
					className="relative cursor-crosshair touch-none border-b border-line select-none"
					{...pointer}
				>
					{ticks.map((tick) => (
						<span
							key={tick.label}
							aria-hidden
							className="absolute inset-x-0 border-t border-dashed border-line"
							style={{ top: pct(tick.y) }}
						/>
					))}
					{loading ? <Skeleton className="absolute inset-0" /> : children}
				</div>
				<span />
				{axis}
			</div>
		</section>
	);
}
