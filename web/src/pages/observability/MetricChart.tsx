import type { ReactNode } from "react";
import { Area, AreaChart, CartesianGrid, ReferenceLine, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/design/Chart";
import { DAY_MS, formatHourMinute, formatSlashDate } from "@/lib/time";
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

type Datum = Record<string, number>;

export function MetricCard({
	title,
	unit,
	big,
	delta,
	deltaBad,
	bigNote,
	actions,
	span,
	children,
	muted,
}: {
	title: ReactNode;
	unit: ReactNode;
	big: string;
	delta: string;
	deltaBad: boolean;
	bigNote: string;
	actions?: ReactNode;
	span?: boolean;
	children: ReactNode;
	muted?: boolean;
}) {
	return (
		<section
			className={cn(
				"flex min-w-0 flex-col gap-2.5 rounded-lg border border-line bg-background px-4 pt-3.5 pb-2.5",
				span === true && "col-span-full",
			)}
		>
			<div className="flex items-start gap-2">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="flex items-baseline gap-1.5">
						<span className={cn("font-semibold", muted === true ? "text-fg4" : "text-fg2")}>{title}</span>
						<span className="text-sm text-fg3">{unit}</span>
					</span>
					<span className="flex items-baseline gap-2">
						<span className={cn("text-[22px] font-semibold tracking-[-0.01em] tabular-nums", muted === true && "text-fg4")}>
							{big}
						</span>
						{delta !== "" && (
							<span className={cn("text-sm font-medium", deltaBad ? "text-bad-fg" : "text-ok-fg")}>{delta}</span>
						)}
						<span className="text-sm text-fg3">{bigNote}</span>
					</span>
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}

export function MetricChart({
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
	const data = [...byT.values()].sort((a, b) => (a.t ?? 0) - (b.t ?? 0));
	const config: ChartConfig = Object.fromEntries(series.map((s, i) => [`s${String(i)}`, { label: s.name, color: s.color }]));
	const span = to - from;
	const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => from + f * span);
	const tickLabel = (v: number) => {
		if (Math.abs(v - to) < span * 0.01) return "now";
		return span > DAY_MS ? formatSlashDate(v) : formatHourMinute(v);
	};
	const single = series.length === 1;

	return (
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
					tick={{ fontSize: 11, fill: "var(--fg4)" }}
				/>
				<YAxis
					domain={[0, max]}
					ticks={[0, max / 2, max]}
					tickFormatter={fmt}
					tickLine={false}
					axisLine={false}
					width={40}
					tick={{ fontSize: 11, fill: "var(--fg4)" }}
				/>
				<ChartTooltip
					cursor={{ stroke: "var(--fg3)", strokeWidth: 1 }}
					content={({ active, label }) => {
						const t = Number(label);
						const row = byT.get(t);
						if (!active || row === undefined) return null;
						const items = series
							.map((s, i) => ({ s, v: row[`s${String(i)}`] }))
							.filter((x): x is { s: ChartSeries; v: number } => x.v !== undefined)
							.sort((a, b) => b.v - a.v);
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
					}}
				/>
				{limit !== null && (
					<ReferenceLine
						y={limit.v}
						stroke="var(--err)"
						strokeDasharray="5 4"
						label={{ value: limit.label, position: "insideTopRight", fill: "var(--err)", fontSize: 10.5 }}
					/>
				)}
				{markers.map((m) => (
					<ReferenceLine
						key={`${String(m.t)}-${m.label}`}
						x={m.t}
						stroke={m.color}
						strokeDasharray="3 3"
						strokeOpacity={0.8}
						label={{ value: m.label, position: "insideTopLeft", fill: m.color, fontSize: 10.5, fontWeight: 600 }}
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
	);
}
