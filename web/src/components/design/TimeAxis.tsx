import { useTimePreferences } from "@/lib/use-time-preferences";
import { formatAxisTime, timeAxis } from "@/lib/time";
import { cn } from "@/lib/utils";
import { useWidth } from "@/hooks/useWidth";

export function TimeAxis({ from, to }: { from: number; to: number }) {
	const timePref = useTimePreferences();
	const [axisWidth, measureAxis] = useWidth();
	const [labelWidth, measureLabel] = useWidth();
	const widest = timeAxis(from, to, timePref)
		.map((t) => formatAxisTime(t, from, to, timePref))
		.reduce((a, b) => (b.length > a.length ? b : a), "");
	// Edge labels anchor to their tick, so ticks sit two label widths apart to leave a gap.
	const ticks = timeAxis(from, to, timePref, labelWidth > 0 ? Math.floor(axisWidth / (2 * labelWidth)) : undefined);
	return (
		<div ref={measureAxis} aria-hidden className="relative h-4 font-mono text-[10.5px] text-fg3">
			<span ref={measureLabel} className="invisible absolute whitespace-nowrap">
				{widest}
			</span>
			{ticks.map((t, i) => (
				<span
					key={t}
					className={cn(
						"absolute whitespace-nowrap",
						i === 0 ? "translate-x-0" : i === ticks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
					)}
					style={{ left: `${String(((t - from) / (to - from)) * 100)}%` }}
				>
					{formatAxisTime(t, from, to, timePref)}
				</span>
			))}
		</div>
	);
}
