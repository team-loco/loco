import { Skeleton } from "@/components/design/Skeleton";

import type { MetricPointPct } from "./useRegionMetric";

const W = 1000;
const H = 100;

export function MetricChart({
	label,
	current,
	unit,
	points,
	isLoading,
	emptyText,
	limitLine,
	from,
	last,
}: {
	label: string;
	current: string;
	unit: string;
	points: MetricPointPct[];
	isLoading: boolean;
	emptyText: string;
	limitLine: boolean;
	from: string;
	last: boolean;
}) {
	const max = Math.max(100, ...points.map((p) => p.pct));
	const t0 = points[0]?.time ?? 0;
	const t1 = points.at(-1)?.time ?? 1;
	const span = Math.max(1, t1 - t0);
	const xy = points.map((p) => {
		const x = points.length === 1 ? W : ((p.time - t0) / span) * W;
		const y = H - (p.pct / max) * (H - 6) - 3;
		return `${x.toFixed(1)},${y.toFixed(1)}`;
	});
	const line = xy.map((p, i) => `${i === 0 ? "M" : "L"}${p}`).join(" ");
	const limitY = H - (100 / max) * (H - 6) - 3;

	return (
		<div className={`flex min-w-0 flex-col gap-1.5 px-4 py-3.5 ${last ? "" : "border-r border-line"}`}>
			<div className="flex items-baseline gap-1.5">
				<span className="flex-1 text-sm text-fg3">{label}</span>
				<span className="text-xl font-semibold tabular-nums">{current}</span>
				<span className="text-sm text-fg3">{unit}</span>
			</div>
			<div className="relative h-24">
				{isLoading ? (
					<Skeleton className="absolute inset-0" />
				) : points.length === 0 ? (
					<div className="absolute inset-0 flex items-center justify-center rounded-sm border border-dashed border-line text-sm text-fg4">
						{emptyText}
					</div>
				) : (
					<svg viewBox={`0 0 ${W.toString()} ${H.toString()}`} preserveAspectRatio="none" className="absolute inset-0 size-full">
						{[0.25, 0.5, 0.75].map((f) => (
							<line key={f} x1={0} x2={W} y1={H * f} y2={H * f} className="stroke-line" strokeWidth={1} vectorEffect="non-scaling-stroke" />
						))}
						<path d={`${line} L${W.toString()},${H.toString()} L0,${H.toString()} Z`} className="fill-primary" opacity={0.1} />
						<path d={line} fill="none" className="stroke-primary" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
						{limitLine && (
							<line
								x1={0}
								x2={W}
								y1={limitY}
								y2={limitY}
								className="stroke-bad-fg"
								strokeWidth={1}
								strokeDasharray="4 4"
								vectorEffect="non-scaling-stroke"
							/>
						)}
					</svg>
				)}
			</div>
			<div className="flex justify-between text-xs text-fg4">
				<span>{from}</span>
				<span>now</span>
			</div>
		</div>
	);
}
