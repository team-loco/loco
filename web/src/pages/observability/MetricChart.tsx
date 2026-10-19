import type { ReactNode } from "react";
import { ListIcon } from "lucide-react";
import { Area, AreaChart, CartesianGrid, ReferenceLine, XAxis, YAxis } from "recharts";

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
import { formatMetricTime, timeAxis } from "@/lib/compute-chart";
import { cn } from "@/lib/utils";

import { fmtTs } from "./format";

export interface ChartSeries {
	name: string;
	color: string;
	points: { t: number; v: number }[];
}

export interface ChartMarker {
	t: number;
	label: string;
	title: string;
	color: string;
}

function MetricEvents({ title, markers, onPick }: { title: string; markers: ChartMarker[]; onPick: (t: number) => void }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger render={<Button variant="ghost" size="sm" className="self-start" />} aria-label={`View ${title} timeline events`}>
				<ListIcon className="text-fg3" />
				{markers.length} timeline {markers.length === 1 ? "event" : "events"}
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="max-h-64 w-[min(360px,calc(100vw-32px))] overflow-y-auto">
				<DropdownMenuGroup>
					<DropdownMenuLabel>View logs at an event</DropdownMenuLabel>
					{markers.toSorted((a, b) => b.t - a.t).map((marker) => (
						<DropdownMenuItem key={`${String(marker.t)}-${marker.label}`} onClick={() => { onPick(marker.t); }} className="h-auto items-start py-2">
							<span className="mt-1.5 size-2 shrink-0 rounded-[2px]" style={{ background: marker.color }} />
							<span className="flex min-w-0 flex-col gap-0.5">
								<span className="break-words whitespace-normal">{marker.title}</span>
								<time dateTime={new Date(marker.t).toISOString()} className="text-sm text-fg3 tabular-nums">{fmtTs(marker.t)}</time>
							</span>
						</DropdownMenuItem>
					))}
				</DropdownMenuGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

type Datum = { t: number; [series: string]: number };

function MetricTooltip({
	active,
	label,
	byT,
	series,
	fmt,
}: {
	active?: boolean;
	label?: string | number;
	byT: Map<number, Datum>;
	series: ChartSeries[];
	fmt: (v: number) => string;
}) {
	const t = Number(label);
	const row = byT.get(t);
	if (active !== true || row === undefined) return null;
	const items = series
		.map((s, i) => ({ s, v: row[`s${String(i)}`] }))
		.filter((x): x is { s: ChartSeries; v: number } => x.v !== undefined)
		.toSorted((a, b) => b.v - a.v);
	return (
		<div className="min-w-[150px] rounded-lg border border-line bg-background px-2.5 py-2 text-sm shadow-popover">
			<div className="mb-1.5 text-fg3">{fmtTs(t)}</div>
			{items.map(({ s, v }) => (
				<div key={s.name} className="flex h-5 items-center gap-2">
					<span className="size-2 rounded-[2px]" style={{ background: s.color }} />
					<span className="flex-1 text-fg2">{s.name}</span>
					<span className="font-semibold tabular-nums">{fmt(v)}</span>
				</div>
			))}
			<div className="mt-1.5 border-t border-line pt-1.5 text-xs text-fg3">Click for logs</div>
		</div>
	);
}

export function MetricCard({
	title,
	unit,
	big,
	delta,
	deltaBad,
	bigNote,
	children,
}: {
	title: ReactNode;
	unit: ReactNode;
	big: string;
	delta: string;
	deltaBad: boolean;
	bigNote: string;
	children: ReactNode;
}) {
	return (
		<section className="flex min-w-0 flex-col gap-2.5 rounded-lg border border-line bg-background px-4 pt-3.5 pb-2.5">
			<div className="flex items-start gap-2">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="flex items-baseline gap-1.5">
						<span className="font-semibold text-fg2">{title}</span>
						<span className="text-sm text-fg3">{unit}</span>
					</span>
					<span className="flex items-baseline gap-2">
						<span className="text-[22px] font-semibold tracking-[-0.01em] tabular-nums">
							{big}
						</span>
						{delta !== "" && (
							<span className={cn("text-sm font-medium", deltaBad ? "text-bad-fg" : "text-ok-fg")}>{delta}</span>
						)}
						<span className="text-sm text-fg3">{bigNote}</span>
					</span>
				</div>
			</div>
			{children}
		</section>
	);
}

export function MetricChart({
	title,
	series,
	from,
	to,
	max,
	fmt,
	limit,
	markers,
	height,
	onPick,
}: {
	title: string;
	series: ChartSeries[];
	from: number;
	to: number;
	max: number;
	fmt: (v: number) => string;
	limit: { v: number; label: string } | null;
	markers: ChartMarker[];
	height: number;
	onPick: (t: number) => void;
}) {
	const byT = new Map<number, Datum>();
	series.forEach((s, i) => {
		for (const p of s.points) {
			const row = byT.get(p.t) ?? { t: p.t };
			row[`s${String(i)}`] = p.v;
			byT.set(p.t, row);
		}
	});
	const data = [...byT.values()].toSorted((a, b) => a.t - b.t);
	const config: ChartConfig = Object.fromEntries(series.map((s, i) => [`s${String(i)}`, { label: s.name, color: s.color }]));
	const ticks = timeAxis(from, to);
	const tickLabel = (value: number) => formatMetricTime(value, from, to);
	const single = series.length === 1;

	return (
		<>
			<ChartContainer config={config} className="aspect-auto w-full cursor-crosshair" style={{ height }}>
				<AreaChart
					data={data}
					margin={{ top: 6, right: 4, bottom: 0, left: 0 }}
					onClick={(state) => {
						const t = Number(state.activeLabel);
						if (Number.isFinite(t)) onPick(t);
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
						tickLine={false}
						axisLine={{ stroke: "var(--line)" }}
						tick={{ fontSize: 11, fill: "var(--fg3)" }}
					/>
					<YAxis
						domain={[0, max]}
						ticks={[0, max / 4, max / 2, max * 3 / 4, max]}
						tickFormatter={fmt}
						tickLine={false}
						axisLine={false}
						width={48}
						tick={{ fontSize: 11, fill: "var(--fg3)" }}
					/>
					<ChartTooltip
						cursor={{ stroke: "var(--fg3)", strokeWidth: 1 }}
						content={<MetricTooltip byT={byT} series={series} fmt={fmt} />}
					/>
					{limit !== null && (
						<ReferenceLine
							y={limit.v}
							stroke="var(--r-fg)"
							strokeDasharray="5 4"
							label={{ value: limit.label, position: "insideTopRight", fill: "var(--r-fg)", fontSize: 10.5 }}
						/>
					)}
					{markers.map((m) => (
						<ReferenceLine
							key={`${String(m.t)}-${m.label}`}
							x={m.t}
							stroke={m.color}
							strokeDasharray="3 3"
							strokeOpacity={0.8}
						/>
					))}
					{series.map((s, i) => (
						<Area
							key={s.name}
							dataKey={`s${String(i)}`}
							name={s.name}
							type="monotone"
							stroke={s.color}
							strokeWidth={1.75}
							fill={s.color}
							fillOpacity={single ? 0.08 : 0}
							dot={false}
							activeDot={{ r: 4, stroke: "var(--bg)", strokeWidth: 2 }}
							isAnimationActive={false}
							connectNulls
						/>
					))}
				</AreaChart>
			</ChartContainer>
		{markers.length > 0 && <MetricEvents title={title} markers={markers} onPick={onPick} />}
		</>
	);
}
