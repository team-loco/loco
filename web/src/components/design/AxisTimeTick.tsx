import { formatAxisTime, type TimePreferences } from "@/lib/time";

interface TickProps {
	className?: string;
	x: number | string;
	y?: number | string;
	payload?: { value: number };
}

/** Use the collision-adjusted label position supplied by Recharts. */
export function axisTimeTick(from: number, to: number, pref: TimePreferences, fontSize = 11) {
	return function AxisTimeTick({ x, y = 0, payload, className }: TickProps) {
		if (payload === undefined) return null;
		return (
			<text
				className={className}
				x={x}
				y={Number(y) + 10}
				textAnchor="middle"
				fontSize={fontSize}
				fill="var(--fg3)"
				fontFamily="var(--font-mono)"
			>
				{formatAxisTime(payload.value, from, to, pref)}
			</text>
		);
	};
}
