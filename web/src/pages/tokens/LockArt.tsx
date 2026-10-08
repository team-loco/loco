import type { CSSProperties } from "react";

const CYCLE = "4.8s cubic-bezier(.65,0,.35,1) infinite";
const STROKE_WIDTH = 1.25;

const LINE = {
	fill: "none",
	strokeWidth: STROKE_WIDTH,
	strokeLinecap: "round",
	strokeLinejoin: "round",
} as const;

const LOCK_STYLE: CSSProperties = { animation: `pe-lock ${CYCLE}` };
const KEY_STYLE: CSSProperties = { animation: `pe-key ${CYCLE}` };
const SHACKLE_STYLE: CSSProperties = { animation: `pe-shackle ${CYCLE}` };
const DOT_STYLE: CSSProperties = { animation: `pe-dot ${CYCLE}` };

const CORNERS: readonly (readonly [number, number, number, number])[] = [
	[6, 6, 12, 12],
	[314, 6, -12, 12],
	[6, 150, 12, -12],
	[314, 150, -12, -12],
];

export function LockArt() {
	return (
		<svg viewBox="0 0 320 156" className="pe-art block w-full max-w-[400px] overflow-visible py-2" aria-hidden="true">
			{CORNERS.map(([x, y, dx, dy]) => (
				<path key={`${x.toString()}-${y.toString()}`} d={`M${x.toString()},${(y + dy).toString()} V${y.toString()} H${(x + dx).toString()}`} stroke="var(--line2)" {...LINE} />
			))}
			<path d="M24,80 H296" stroke="var(--line)" strokeDasharray="1 4" {...LINE} />
			<g style={KEY_STYLE}>
				<circle cx={42} cy={80} r={13} stroke="var(--fg2)" {...LINE} />
				<circle cx={42} cy={80} r={4} stroke="var(--fg3)" {...LINE} />
				<path d="M55,80 H132 M112,80 V89 H118 V85 H124 V89 H132 V80" stroke="var(--fg2)" {...LINE} />
			</g>
			<g style={SHACKLE_STYLE}>
				<path d="M200,62 V48 a18,18 0 0 1 36,0 V62" stroke="var(--fg3)" {...LINE} style={LOCK_STYLE} />
			</g>
			<rect x={188} y={62} width={60} height={48} rx={6} fill="var(--bg)" stroke="var(--fg3)" strokeWidth={STROKE_WIDTH} style={LOCK_STYLE} />
			<circle cx={218} cy={82} r={4} stroke="var(--fg3)" {...LINE} style={LOCK_STYLE} />
			<path d="M218,86 V94" stroke="var(--fg3)" {...LINE} style={LOCK_STYLE} />
			<circle cx={248} cy={62} r={3} fill="var(--accent)" style={DOT_STYLE} />
		</svg>
	);
}
