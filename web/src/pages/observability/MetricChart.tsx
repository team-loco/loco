import { loneSampleDot } from "@/lib/chart-dot";
import { axisTimeTick } from "@/components/design/AxisTimeTick";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { Fragment, useRef, useState, type ReactNode } from "react";
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
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { formatPercent, quarterAxis } from "@/lib/compute-chart";
import { limitAt, type LimitStep } from "@/lib/limit-history";
import { trafficAxis } from "@/lib/traffic";
import { cn } from "@/lib/utils";
import { formatAxisTime, timeAxis } from "@/lib/time";

import { fmtTs } from "./format";
import { markerGroups } from "./metric-markers";

export interface ChartSeries {
	key: string;
	name: string;
	color: string;
	dashed: boolean;
	dash?: string;
	limits: LimitStep[];
	points: { t: number; v: number }[];
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
	sub?: string;
	/** Starts a new group in the menu. */
	separated?: boolean;
	run: () => void;
}

const PERCENT = 100;
const DASH = "4 3";
const TOOLTIP_ROWS = 8;
const DIMMED = 0.12;
const X_AXIS_HEIGHT = 28;
const TOP_MARGIN = 14;
const MARGIN = 4;
// Labels of deploys past this share of the range sit left of their line.
const FLIP_SHARE = 0.8;
const LABEL_CHAR_PX = 6;
const LABEL_PAD_PX = 4;
const LABEL_HEIGHT_PX = 14;

type Datum = { t: number; [series: string]: number };

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
	unit,
}: {
	active?: boolean;
	label?: string | number;
	byT: Map<number, Datum>;
	series: ChartSeries[];
	fmt: (v: number) => string;
	highlight: string | null;
	unit: string | undefined;
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
				<span>{unit ?? "% of limit"}</span>
			</div>
			{shown.map(({ s, v }) => (
				<div
					key={s.key}
					data-tooltip-row
					className={cn("flex h-5 items-center gap-2 whitespace-nowrap", highlight === s.key && "font-medium")}
				>
					<span className="size-2 shrink-0 rounded-[2px]" style={{ background: s.color }} />
					<span className="max-w-[160px] flex-1 truncate text-fg2">{s.name}</span>
					{unit === undefined ? (
						<>
							<span className="shrink-0 font-mono tabular-nums">
								{fmt((v / PERCENT) * (limitAt(s.limits, t) ?? 0))}
							</span>
							<span className="w-12 shrink-0 text-right font-mono text-fg3 tabular-nums">{formatPercent(v)}</span>
						</>
					) : (
						<span className="shrink-0 font-mono tabular-nums">{fmt(v)}</span>
					)}
				</div>
			))}
			{more > 0 && <div className="mt-1.5 h-5 text-fg3">+{more} more</div>}
		</div>
	);
}

function PinMenu({
	t,
	anchor,
	actions,
	onClose,
}: {
	t: number;
	anchor: () => DOMRect;
	actions: PinAction[];
	onClose: () => void;
}) {
	const timePref = useTimePreferences();
	return (
		<DropdownMenuContent
			align="start"
			anchor={{ getBoundingClientRect: anchor }}
			sideOffset={8}
			className="w-[190px]"
			aria-label={`Around ${fmtTs(t, false, timePref)}`}
		>
			<DropdownMenuGroup>
				<DropdownMenuLabel>{fmtTs(t, false, timePref)}</DropdownMenuLabel>
				{actions.map((a) => (
					<Fragment key={a.key}>
						{a.separated === true && <DropdownMenuSeparator />}
						<DropdownMenuItem
							onClick={() => {
								onClose();
								a.run();
							}}
							className="items-start"
						>
							{a.icon}
							<span className="flex min-w-0 flex-col">
								<span>{a.label}</span>
								{a.sub && <span className="text-[12px] text-fg3">{a.sub}</span>}
							</span>
						</DropdownMenuItem>
					</Fragment>
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
	fmt,
	highlight,
	markers,
	height,
	actions,
	unit,
	markerMenu = false,
}: {
	title: string;
	series: ChartSeries[];
	from: number;
	to: number;
	fmt: (v: number) => string;
	highlight: string | null;
	markers: ChartMarker[];
	height: number;
	actions: (t: number) => PinAction[];
	/** Plots values in this unit from zero; without one, values are percentages of each series' limit. */
	unit?: string;
	/** Lists the markers in a menu under the chart. */
	markerMenu?: boolean;
}) {
	const timePref = useTimePreferences();
	const [pin, setPin] = useState<{ t: number; dx: number; dy: number } | null>(null);
	const triggerRef = useRef<HTMLDivElement>(null);
	const [nearest, setNearest] = useState<string | null>(null);
	const peaks = series.map((s) => ({ key: s.key, peak: Math.max(...s.points.map((p) => p.v)) }));
	const peak = Math.max(0, ...peaks.map((p) => p.peak));
	const traffic = unit === undefined ? null : trafficAxis(peak, unit);
	const axis =
		traffic === null
			? { ...quarterAxis(peak), format: String }
			: { ticks: traffic.ticks, maximum: traffic.top, format: traffic.format };

	const highlightKey = highlight ?? nearest;
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
				<DropdownMenuTrigger nativeButton={false} render={<div ref={triggerRef} />} aria-label={`${title} over time`}>
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
								right: MARGIN,
								bottom: 4,
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
							onClick={(state, event) => {
								const t = data[Number(state.activeTooltipIndex)]?.t ?? Number(state.activeLabel);
								if (!Number.isFinite(t)) return;
								const box = triggerRef.current?.getBoundingClientRect();
								if (!box) return;
								setPin(pin?.t === t ? null : { t, dx: event.clientX - box.left, dy: event.clientY - box.top });
							}}
						>
							<CartesianGrid vertical={false} stroke="var(--line)" />
							<XAxis
								dataKey="t"
								type="number"
								scale="time"
								domain={[from, to]}
								allowDataOverflow
								ticks={ticks}
								interval="preserveStartEnd"
								minTickGap={24}
								tickLine={false}
								height={X_AXIS_HEIGHT}
								axisLine={{ stroke: "var(--line)" }}
								tickFormatter={(time: number) => formatAxisTime(time, from, to, timePref)}
								tick={axisTimeTick(from, to, timePref, 10.5)}
							/>
							<YAxis
								domain={[0, axis.maximum]}
								ticks={axis.ticks}
								tickFormatter={axis.format}
								tickLine={false}
								axisLine={false}
								width={72}
								tick={{
									fontSize: 10.5,
									fill: "var(--fg3)",
									fontFamily: "var(--font-mono)",
									dy: 0,
								}}
							/>
							{pin === null && (
								<ChartTooltip
									cursor={{ stroke: "var(--fg4)", strokeWidth: 1 }}
									content={<MetricTooltip byT={byT} series={series} fmt={fmt} highlight={highlightKey} unit={unit} />}
								/>
							)}
							{unit === undefined && (
								<ReferenceLine
									y={PERCENT}
									stroke="color-mix(in oklab, var(--err) 55%, transparent)"
									strokeDasharray="5 4"
									label={{
										value: "limit",
										position: "insideBottomLeft",
										fill: "var(--color-bad-fg)",
										fontSize: 10,
									}}
								/>
							)}
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
							{series.map((s) => {
								const opacity = highlightKey !== null ? (highlightKey === s.key ? 1 : DIMMED) : 1;
								return (
									<Line
										key={s.key}
										dataKey={s.key}
										name={s.name}
										type="monotone"
										stroke={s.color}
										strokeWidth={highlightKey === s.key ? 2.4 : 1.6}
										strokeOpacity={opacity}
										strokeDasharray={s.dash ?? (s.dashed ? DASH : "")}
										dot={loneSampleDot(s.points.length, s.color, opacity)}
										activeDot={{ r: 3.5, stroke: "var(--panel)", strokeWidth: 2 }}
										isAnimationActive={false}
										connectNulls
									/>
								);
							})}
						</LineChart>
					</ChartContainer>
				</DropdownMenuTrigger>
				{pin !== null && (
					<PinMenu
						t={pin.t}
						anchor={() => {
							const box = triggerRef.current?.getBoundingClientRect();
							return new DOMRect((box?.left ?? 0) + pin.dx, (box?.top ?? 0) + pin.dy, 0, 0);
						}}
						actions={actions(pin.t)}
						onClose={() => {
							setPin(null);
						}}
					/>
				)}
			</DropdownMenu>
			{markerMenu && markers.length > 0 && <MetricEvents title={title} markers={markers} actions={actions} />}
		</>
	);
}
