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
import { formatMetricTime, timeAxis } from "@/lib/compute-chart";
import { DAY_MS } from "@/lib/time";

import { fmtTs } from "./format";

export interface ChartSeries {
	key: string;
	name: string;
	color: string;
	dashed: boolean;
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
	sub: string;
	run: () => void;
}

const DASH = "4 3";
const MARGIN = 24;
const WIDE_MARGIN = 44;
const Y_TICKS = 4;

type Datum = { t: number; [series: string]: number };

function MetricEvents({
	title,
	markers,
	actions,
}: {
	title: string;
	markers: ChartMarker[];
	actions: (t: number) => PinAction[];
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="sm" className="self-start" />}
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
										{fmtTs(marker.t)}
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
		.map((s) => ({ s, v: row[s.key] }))
		.filter((x): x is { s: ChartSeries; v: number } => x.v !== undefined)
		.toSorted((a, b) => b.v - a.v);
	return (
		<div className="min-w-[160px] rounded-lg border border-line bg-background px-2.5 py-2 text-[12px] shadow-popover">
			<div className="mb-1.5 font-mono text-[11px] text-fg3">{fmtTs(t)}</div>
			{items.map(({ s, v }) => (
				<div key={s.key} className="flex h-5 items-center gap-2">
					<span className="size-2 rounded-[2px]" style={{ background: s.color }} />
					<span className="flex-1 text-fg2">{s.name}</span>
					<span className="font-mono tabular-nums">{fmt(v)}</span>
				</div>
			))}
			<div className="mt-1.5 border-t border-line pt-1.5 text-[11px] text-fg3">Click for logs, traces and events</div>
		</div>
	);
}

function PinMenu({ t, x, actions, onClose }: { t: number; x: number; actions: PinAction[]; onClose: () => void }) {
	return (
		<DropdownMenuContent
			align="start"
			alignOffset={({ anchor }) => x * anchor.width + 8}
			sideOffset={({ anchor }) => 8 - anchor.height}
			className="w-[250px]"
			aria-label={`Around ${fmtTs(t)}`}
		>
			<DropdownMenuGroup>
				<DropdownMenuLabel>{fmtTs(t)}</DropdownMenuLabel>
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
	max,
	fmt,
	limit,
	markers,
	height,
	actions,
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
	actions: (t: number) => PinAction[];
}) {
	const [pin, setPin] = useState<{ t: number; x: number } | null>(null);
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
	const ticks = timeAxis(from, to);
	const tickLabel = (value: number) => formatMetricTime(value, from, to);
	const yTicks = Array.from({ length: Y_TICKS + 1 }, (_, i) => (max * i) / Y_TICKS);

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
					<ChartContainer config={config} className="aspect-auto w-full cursor-pointer" style={{ height }}>
						<LineChart
							syncId="metrics"
							syncMethod="value"
							data={data}
							margin={{ top: 6, right: to - from >= DAY_MS ? WIDE_MARGIN : MARGIN, bottom: 0, left: 0 }}
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
								tickLine={false}
								axisLine={{ stroke: "var(--line)" }}
								tick={{ fontSize: 10.5, fill: "var(--fg3)", fontFamily: "var(--font-mono)" }}
							/>
							<YAxis
								domain={[0, max]}
								ticks={yTicks}
								tickFormatter={fmt}
								tickLine={false}
								axisLine={false}
								width={44}
								tick={{ fontSize: 10.5, fill: "var(--fg3)", fontFamily: "var(--font-mono)" }}
							/>
							{pin === null && (
								<ChartTooltip
									cursor={{ stroke: "var(--fg4)", strokeWidth: 1 }}
									content={<MetricTooltip byT={byT} series={series} fmt={fmt} />}
								/>
							)}
							{limit !== null && (
								<ReferenceLine
									y={limit.v}
									stroke="color-mix(in oklab, var(--err) 55%, transparent)"
									strokeDasharray="5 4"
									label={{ value: limit.label, position: "insideTopLeft", fill: "var(--color-bad-fg)", fontSize: 10 }}
								/>
							)}
							{markers.map((m, i) => (
								<ReferenceLine
									key={`${String(m.t)}-${m.label}`}
									x={m.t}
									stroke={m.color}
									strokeDasharray="3 3"
									strokeOpacity={0.8}
									// Alternate rows so labels of nearby deploys don't overlap.
									label={{
										value: m.label,
										position: i % 2 === 0 ? "insideTopLeft" : "insideBottomLeft",
										fill: m.color,
										fontSize: 10,
									}}
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
									strokeWidth={1.6}
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
