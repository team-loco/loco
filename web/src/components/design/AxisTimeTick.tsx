import { formatAxisTime, type TimePreferences } from "@/lib/time";

interface TickProps {
	x?: number | string;
	y?: number | string;
	index?: number;
	visibleTicksCount?: number;
	payload?: { value: number };
}

/** An XAxis tick renderer that left-aligns the first label and right-aligns the last. */
export function axisTimeTick(from: number, to: number, pref: TimePreferences, fontSize = 11) {
	return function AxisTimeTick({ x = 0, y = 0, index = 0, visibleTicksCount = 0, payload }: TickProps) {
		if (payload === undefined) return null;
		const last = visibleTicksCount > 0 && index === visibleTicksCount - 1;
		const anchor = index === 0 ? "start" : last ? "end" : "middle";
		return (
			<text
				x={x}
				y={Number(y) + 10}
				textAnchor={anchor}
				fontSize={fontSize}
				fill="var(--fg3)"
				fontFamily="var(--font-mono)"
			>
				{formatAxisTime(payload.value, from, to, pref)}
			</text>
		);
	};
}
