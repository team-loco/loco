import { useTimePreferences } from "@/lib/use-time-preferences";
import { useState, type ReactNode } from "react";
import { ListIcon } from "lucide-react";
import { CartesianGrid, Line, LineChart, ReferenceLine, XAxis, YAxis } from "recharts";

import { Button } from "@/components/design/Button";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/design/Chart";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { formatPercent } from "@/lib/compute-chart";
import { cn } from "@/lib/utils";
import { DAY_MS, formatAxisTime, timeAxis } from "@/lib/time";

import { fmtTs } from "./format";
import { markerGroups } from "./metric-markers";

export interface ChartSeries {
	key: string;
	name: string;
	resourceId: string;
	color: string;
	dashed: boolean;
	points: { t: number; v: number; abs: number }[];
}

export interface ChartMarker {
	t: number;
	label: string;
	title: string;
	color: string;
}

export interface PinAction {
	key: string;
	icon: ReactNode;
	label: string;
	sub: string;
	run: () => void;
}

const PERCENT = 100;
const DASH = "4 3";
const TOP_SERIES = 5;
const TOOLTIP_ROWS = 8;
const FADED = 0.3;
const DIMMED = 0.12;
const X_AXIS_HEIGHT = 20;
const TOP_MARGIN = 14;
const MARGIN = 24;
const WIDE_MARGIN = 44;
// Labels of deploys past this share of the range sit left of their line.
const FLIP_SHARE = 0.8;
const LABEL_CHAR_PX = 6;
const LABEL_PAD_PX = 4;
const LABEL_HEIGHT_PX = 14;

type Datum = { t: number; [series: string]: number };

function absOf(s: ChartSeries, t: number): number {
	return s.points.find((p) => p.t === t)?.abs ?? 0;
}

function MarkerLabel({ viewBox, value, flip }: { viewBox?: { x?: number; y?: number }; value: string; flip: boolean }) {
	const x0 = viewBox?.x ?? 0;
	const y = viewBox?.y ?? 0;
	const width = value.length * LABEL_CHAR_PX + LABEL_PAD_PX * 2;
	const x = flip ? x0 - width - 2 : x0 + 2;
	return (
		<g>
			<rect x={x} y={y} width={width} height={LABEL_HEIGHT_PX} rx={3} fill="var(--panel)" />
			<text x={x + LABEL_PAD_PX} y={y + 10} fontSize={10} fill="var(--fg3)" fontFamily="var(--font-mono)">
				{value}
			</text>
		</g>
	);
}

function MetricEvents({
	title,
	markers,
	actions,
}: {
	title: string;
	markers: ChartMarker[];
	actions: (t: number) => PinAction[];
}) {
	const timePref = useTimePreferences();
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="sm" className="-mt-2 self-start" />}
				aria-label={`${title} timeline events`}
			>
				<ListIcon className="text-fg3" />
				{markers.length} {markers.length === 1 ? "deploy" : "deploys"} in range
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="max-h-64 w-[min(360px,calc(100vw-32px))] overflow-y-auto">
				<DropdownMenuGroup>
					<DropdownMenuLabel>Logs around a deploy</DropdownMenuLabel>
					{markers
						.toSorted((a, b) => b.t - a.t)
						.map((marker) => (
							<DropdownMenuItem
								key={`${String(marker.t)}-${marker.label}`}
								onClick={() => {
									actions(marker.t)
										.find((a) => a.key === "logs")
										?.run();
								}}
								className="h-auto items-start py-2"
							>
								<span className="mt-1.5 size-2 shrink-0 rounded-[2px]" style={{ background: marker.color }} />
								<span className="flex min-w-0 flex-col gap-0.5">
									<span className="break-words whitespace-normal">{marker.title}</span>
									<time dateTime={new Date(marker.t).toISOString()} className="text-sm text-fg3 tabular-nums">
										{fmtTs(marker.t, false, timePref)}
									</time>
								</span>
							</DropdownMenuItem>
						))}
				</DropdownMenuGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function MetricTooltip({
	active,
	label,
	byT,
	series,
	fmt,
	highlight,
}: {
	active?: boolean;
	label?: string | number;
	byT: Map<number, Datum>;
	series: ChartSeries[];
	fmt: (v: number) => string;
	highlight: string | null;
}) {
	const timePref = useTimePreferences();
	const t = Number(label);
	const row = byT.get(t);
	if (active !== true || row === undefined) return null;
	const items = series
		.map((s) => ({ s, v: row[s.key] }))
		.filter((x): x is { s: ChartSeries; v: number } => x.v !== undefined)
		.toSorted((a, b) => b.v - a.v);
	const shown = items.slice(0, TOOLTIP_ROWS);
	const more = items.length - shown.length;
	return (
		<div className="min-w-[200px] rounded-lg border border-line bg-background px-2.5 py-2 text-[12px] shadow-popover">
			<div className="mb-1.5 flex items-center justify-between gap-3 font-mono text-[11px] whitespace-nowrap text-fg3">
				<span>{fmtTs(t, false, timePref)}</span>
				<span>% of limit</span>
			</div>
			{shown.map(({ s, v }) => (
				<div
					key={s.key}
					data-tooltip-row
					className={cn("flex h-5 items-center gap-2 whitespace-nowrap", highlight === s.key && "font-medium")}
				>
					<span className="size-2 shrink-0 rounded-[2px]" style={{ background: s.color }} />
					<span className="max-w-[160px] flex-1 truncate text-fg2">{s.name}</span>
					<span className="shrink-0 font-mono tabular-nums">{fmt(absOf(s, t))}</span>
					<span className="w-12 shrink-0 text-right font-mono text-fg3 tabular-nums">{formatPercent(v)}</span>
				</div>
			))}
			{more > 0 && <div className="h-5 text-fg3">+{more} more</div>}
			<div className="mt-1.5 border-t border-line pt-1.5 text-[11px] text-fg3">Click for logs, traces and events</div>
		</div>
	);
}

function PinMenu({ t, x, actions, onClose }: { t: number; x: number; actions: PinAction[]; onClose: () => void }) {
	const timePref = useTimePreferences();
	return (
		<DropdownMenuContent
			align="start"
			alignOffset={({ anchor }) => x * anchor.width + 8}
			sideOffset={({ anchor }) => 8 - anchor.height}
			className="w-[250px]"
			aria-label={`Around ${fmtTs(t, false, timePref)}`}
		>
			<DropdownMenuGroup>
				<DropdownMenuLabel>{fmtTs(t, false, timePref)}</DropdownMenuLabel>
				{actions.map((a) => (
					<DropdownMenuItem
						key={a.key}
						onClick={() => {
							onClose();
							a.run();
						}}
						className="items-start"
					>
						{a.icon}
						<span className="flex min-w-0 flex-col">
							<span>{a.label}</span>
							<span className="text-[12px] text-fg3">{a.sub}</span>
						</span>
					</DropdownMenuItem>
				))}
			</DropdownMenuGroup>
		</DropdownMenuContent>
	);
}

export function MetricChart({
	title,
	series,
	from,
	to,
	axis,
	fmt,
	highlightId,
	markers,
	height,
	actions,
}: {
	title: string;
	series: ChartSeries[];
	from: number;
	to: number;
	axis: { maximum: number; ticks: number[] };
	fmt: (v: number) => string;
	highlightId: string | null;
	markers: ChartMarker[];
	height: number;
	actions: (t: number) => PinAction[];
}) {
	const timePref = useTimePreferences();
	const [pin, setPin] = useState<{ t: number; x: number } | null>(null);
	const [nearest, setNearest] = useState<string | null>(null);
	const top = new Set(
		series
			.map((s) => ({ key: s.key, peak: Math.max(...s.points.map((p) => p.v)) }))
			.toSorted((a, b) => b.peak - a.peak)
			.slice(0, TOP_SERIES)
			.map((x) => x.key),
	);
	const highlightKey = series.find((s) => s.resourceId === highlightId)?.key ?? nearest;
	const byT = new Map<number, Datum>();
	for (const s of series) {
		for (const p of s.points) {
			const row = byT.get(p.t) ?? { t: p.t };
			row[s.key] = p.v;
			byT.set(p.t, row);
		}
	}
	const data = [...byT.values()].toSorted((a, b) => a.t - b.t);
	const config: ChartConfig = Object.fromEntries(series.map((s) => [s.key, { label: s.name, color: s.color }]));
	const ticks = timeAxis(from, to, timePref);
	const tickLabel = (value: number) => formatAxisTime(value, from, to, timePref);
	const groups = markerGroups(markers, from, to);

	return (
		<>
			<DropdownMenu
				modal={false}
				open={pin !== null}
				onOpenChange={(open) => {
					if (!open) setPin(null);
				}}
			>
				<DropdownMenuTrigger nativeButton={false} render={<div />} aria-label={`${title} over time`}>
					<ChartContainer
						config={config}
						className="aspect-auto w-full cursor-pointer [&_.recharts-wrapper]:cursor-pointer!"
						style={{ height }}
					>
						<LineChart
							syncId="metrics"
							syncMethod="value"
							data={data}
							margin={{
								top: TOP_MARGIN,
								right: to - from >= DAY_MS ? WIDE_MARGIN : MARGIN,
								bottom: 0,
								left: 0,
							}}
							onMouseLeave={() => {
								setNearest(null);
							}}
							onMouseMove={(state) => {
								const y = state.activeCoordinate?.y;
								const row = data[Number(state.activeTooltipIndex)];
								if (y === undefined || row === undefined) return;
								const level = (1 - (y - TOP_MARGIN) / (height - TOP_MARGIN - X_AXIS_HEIGHT)) * axis.maximum;
								let best: { key: string; gap: number } | null = null;
								for (const s of series) {
									const v = row[s.key];
									if (v !== undefined && (best === null || Math.abs(v - level) < best.gap)) {
										best = { key: s.key, gap: Math.abs(v - level) };
									}
								}
								setNearest(best?.key ?? null);
							}}
							onClick={(state) => {
								const t = data[Number(state.activeTooltipIndex)]?.t ?? Number(state.activeLabel);
								if (!Number.isFinite(t)) return;
								setPin(pin?.t === t ? null : { t, x: (t - from) / Math.max(1, to - from) });
							}}
						>
							<CartesianGrid vertical={false} stroke="var(--line)" />
							<XAxis
								dataKey="t"
								type="number"
								scale="time"
								domain={[from, to]}
								ticks={ticks}
								tickFormatter={tickLabel}
								interval="preserveStartEnd"
								tickLine={false}
								height={X_AXIS_HEIGHT}
								axisLine={{ stroke: "var(--line)" }}
								tick={{
									fontSize: 10.5,
									fill: "var(--fg3)",
									fontFamily: "var(--font-mono)",
								}}
							/>
							<YAxis
								domain={[0, axis.maximum]}
								ticks={axis.ticks}
								tickFormatter={(v: number) => `${String(v)}%`}
								tickLine={false}
								axisLine={false}
								mirror
								tick={{
									fontSize: 10.5,
									fill: "var(--fg3)",
									fontFamily: "var(--font-mono)",
									dy: -6,
								}}
							/>
							{pin === null && (
								<ChartTooltip
									cursor={{ stroke: "var(--fg4)", strokeWidth: 1 }}
									content={<MetricTooltip byT={byT} series={series} fmt={fmt} highlight={highlightKey} />}
								/>
							)}
							<ReferenceLine
								y={PERCENT}
								stroke="color-mix(in oklab, var(--err) 55%, transparent)"
								strokeDasharray="5 4"
								label={{
									value: "limit",
									position: "insideTopLeft",
									fill: "var(--color-bad-fg)",
									fontSize: 10,
								}}
							/>
							{groups.map((g) => (
								<ReferenceLine
									key={g.t}
									x={g.t}
									stroke="var(--fg4)"
									strokeDasharray="3 3"
									label={<MarkerLabel value={g.label} flip={(g.t - from) / Math.max(1, to - from) > FLIP_SHARE} />}
								/>
							))}
							{pin !== null && <ReferenceLine x={pin.t} stroke="var(--fg3)" />}
							{series.map((s) => (
								<Line
									key={s.key}
									dataKey={s.key}
									name={s.name}
									type="monotone"
									stroke={s.color}
									strokeWidth={highlightKey === s.key ? 2.4 : 1.6}
									strokeOpacity={
										highlightKey !== null ? (highlightKey === s.key ? 1 : DIMMED) : top.has(s.key) ? 1 : FADED
									}
									{...(s.dashed ? { strokeDasharray: DASH } : {})}
									dot={false}
									activeDot={{ r: 3.5, stroke: "var(--panel)", strokeWidth: 2 }}
									isAnimationActive={false}
									connectNulls
								/>
							))}
						</LineChart>
					</ChartContainer>
				</DropdownMenuTrigger>
				{pin !== null && (
					<PinMenu
						t={pin.t}
						x={pin.x}
						actions={actions(pin.t)}
						onClose={() => {
							setPin(null);
						}}
					/>
				)}
			</DropdownMenu>
			{markers.length > 0 && <MetricEvents title={title} markers={markers} actions={actions} />}
		</>
	);
}
