import { formatAxisTime, timeAxis } from "@/lib/time";
import { cn } from "@/lib/utils";

export function TimeAxis({ from, to }: { from: number; to: number }) {
	const ticks = timeAxis(from, to);
	return (
		<div aria-hidden className="relative h-4 font-mono text-[10.5px] text-fg3">
			{ticks.map((t, i) => (
				<span
					key={t}
					className={cn(
						"absolute whitespace-nowrap",
						i === 0 ? "translate-x-0" : i === ticks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
					)}
					style={{ left: `${String(((t - from) / (to - from)) * 100)}%` }}
				>
					{formatAxisTime(t, from, to)}
				</span>
			))}
		</div>
	);
}
