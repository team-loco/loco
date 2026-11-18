import { cn } from "@/lib/utils";

const W = 200;
const H = 40;

/** A filled line of `values`, scaled to its own peak, drawn in the current text colour. */
export function Sparkline({ values, fill = 0.08, className }: { values: number[]; fill?: number; className?: string }) {
	if (values.length < 2) return <div aria-hidden="true" className={className} />;
	const peak = Math.max(...values);
	const points = values.map((v, i) => [(i / (values.length - 1)) * W, H - 2 - (peak === 0 ? 0 : v / peak) * (H - 6)]);
	const line = points.map(([x = 0, y = 0], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
	return (
		<svg
			viewBox={`0 0 ${W.toString()} ${H.toString()}`}
			preserveAspectRatio="none"
			aria-hidden="true"
			className={cn("block w-full overflow-visible", className)}
		>
			<path d={`${line} L${W.toString()} ${H.toString()} L0 ${H.toString()} Z`} fill="currentColor" opacity={fill} />
			<path d={line} fill="none" stroke="currentColor" strokeWidth={1.4} vectorEffect="non-scaling-stroke" />
		</svg>
	);
}
