import type { ReactNode } from "react";
import { ArrowUpRightIcon, HistoryIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { formatSpan, windowSpan } from "@/lib/obs-time";
import type { ObservabilityView } from "@/lib/routes";
import { DOCS_URL } from "@/pages/splash/links";

import { useObs } from "./context";

const DOCS_PATH = `${DOCS_URL}/operations/applications/`;

const STRONG = "color-mix(in oklab, var(--fg) 42%, var(--bg))";
const SOFT = "color-mix(in oklab, var(--fg) 22%, var(--bg))";

const LOG_ROWS: { y: number; w: number; mark: string }[] = [
	{ y: 28, w: 48, mark: SOFT },
	{ y: 39.5, w: 36, mark: SOFT },
	{ y: 51, w: 54, mark: "var(--warn)" },
	{ y: 62.5, w: 30, mark: SOFT },
	{ y: 74, w: 44, mark: "var(--err)" },
	{ y: 85.5, w: 38, mark: SOFT },
];
const SPANS: { x: number; y: number; w: number; fill: string }[] = [
	{ x: 142, y: 30, w: 82, fill: "var(--info)" },
	{ x: 148, y: 42, w: 48, fill: STRONG },
	{ x: 154, y: 54, w: 24, fill: STRONG },
	{ x: 180, y: 54, w: 14, fill: STRONG },
	{ x: 198, y: 66, w: 22, fill: STRONG },
	{ x: 204, y: 78, w: 12, fill: SOFT },
];
const EVENT_ROWS: { y: number; w: number; dot: string }[] = [
	{ y: 32, w: 50, dot: "var(--ok)" },
	{ y: 46, w: 40, dot: "var(--warn)" },
	{ y: 60, w: 56, dot: "var(--ok)" },
	{ y: 74, w: 34, dot: "var(--ok)" },
	{ y: 88, w: 46, dot: "var(--err)" },
];
const PODS: [number, number][] = [
	[4, 4],
	[11, 4],
	[4, 11],
	[11, 11],
];

function Panel({ view }: { view: ObservabilityView }) {
	switch (view) {
		case "logs":
			return LOG_ROWS.map((r) => (
				<g key={r.y}>
					<rect x={142} y={r.y} width={14} height={3} rx={1.5} fill={SOFT} />
					<rect x={160} y={r.y} width={8} height={3} rx={1.5} fill={r.mark} />
					<rect x={172} y={r.y} width={r.w} height={3} rx={1.5} fill={STRONG} />
				</g>
			));
		case "metrics":
			return (
				<>
					<path d="M142 30 V92 H226" fill="none" stroke="var(--line2)" />
					<path d="M142 72 H226 M142 52 H226" stroke="var(--line)" strokeDasharray="2 3" />
					<path
						d="M142 86 L152 80 L162 83 L172 70 L182 74 L192 58 L202 64 L212 46 L224 52 L224 92 L142 92 Z"
						fill="var(--info)"
						fillOpacity={0.1}
					/>
					<path
						d="M142 86 L152 80 L162 83 L172 70 L182 74 L192 58 L202 64 L212 46 L224 52"
						fill="none"
						stroke="var(--info)"
						strokeWidth={1.6}
						strokeLinejoin="round"
						strokeLinecap="round"
					/>
					<circle cx={224} cy={52} r={2.4} fill="var(--info)" />
				</>
			);
		case "traces":
			return SPANS.map((s) => (
				<rect key={`${String(s.x)}-${String(s.y)}`} x={s.x} y={s.y} width={s.w} height={6} rx={2} fill={s.fill} />
			));
		case "events":
			return EVENT_ROWS.map((r) => (
				<g key={r.y}>
					<circle cx={146} cy={r.y} r={3} fill={r.dot} />
					<rect x={154} y={r.y - 2} width={r.w} height={3.5} rx={1.75} fill={STRONG} />
					<rect x={154} y={r.y + 4} width={r.w * 0.6} height={2.5} rx={1.25} fill={SOFT} />
					<rect x={214} y={r.y - 2} width={12} height={3} rx={1.5} fill={SOFT} />
				</g>
			));
	}
}

export function ObsArt({ view }: { view: ObservabilityView }) {
	return (
		<svg
			viewBox="14 10 226 100"
			width={300}
			height={133}
			aria-hidden="true"
			className="block max-w-full overflow-visible"
		>
			<path d="M24 60 L58 77 L58 85 L24 68 Z" fill="var(--panel2)" stroke={STRONG} strokeLinejoin="round" />
			<path
				d="M58 77 L92 60 L92 68 L58 85 Z"
				fill="color-mix(in oklab, var(--panel2) 70%, var(--line2))"
				stroke={STRONG}
				strokeLinejoin="round"
			/>
			<path d="M58 43 L92 60 L58 77 L24 60 Z" fill="var(--bg)" stroke={STRONG} strokeLinejoin="round" />
			<g transform="matrix(1.7 0.85 -1.7 0.85 58 43)">
				{PODS.map(([x, y]) => (
					<rect key={`${String(x)}-${String(y)}`} x={x} y={y} width={5} height={5} rx={0.7} fill={STRONG} />
				))}
			</g>
			<path d="M92 62 C112 62 112 60 134 60" fill="none" stroke={SOFT} strokeDasharray="2 2.5" />
			<rect x={134} y={18} width={98} height={84} rx={7} fill="var(--bg)" stroke="var(--line2)" />
			<Panel view={view} />
		</svg>
	);
}

export function ObsHero({
	view,
	title,
	body,
	action,
}: {
	view: ObservabilityView;
	title: ReactNode;
	body: ReactNode;
	action?: ReactNode;
}) {
	return (
		<EmptyHero art={<ObsArt view={view} />} title={title} subtitle={body}>
			<div className="mt-1.5 flex flex-wrap justify-center gap-2">
				{action}
				<Button variant="outline" render={<a href={DOCS_PATH} target="_blank" rel="noreferrer" />}>
					Read the docs
					<ArrowUpRightIcon />
				</Button>
			</div>
		</EmptyHero>
	);
}

export function WidenRangeButton() {
	const { range, setRange, maxMs } = useObs();
	if (windowSpan(range) >= maxMs) return null;
	return (
		<Button
			variant="outline"
			onClick={() => {
				setRange({ kind: "relative", spanMs: maxMs });
			}}
		>
			<HistoryIcon />
			{`Show the past ${formatSpan(maxMs)}`}
		</Button>
	);
}
