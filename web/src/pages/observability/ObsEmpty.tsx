import type { ReactNode } from "react";
import { ArrowUpRightIcon, CheckIcon, CopyIcon, HistoryIcon, SearchXIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { ART_SOFT, ART_STRONG, ClusterArt } from "@/components/design/ClusterArt";
import { EmptyHero } from "@/components/design/EmptyHero";
import { EmptyState } from "@/components/design/EmptyState";
import { useCopy } from "@/hooks/useCopy";
import { formatSpan, windowSpan, type TimeWindow } from "@/lib/obs-time";
import type { ObservabilityView } from "@/lib/routes";
import { DOCS_URL } from "@/pages/splash/links";

import { useObs } from "./context";
import { queryStr } from "./query";

const DOCS_PATH = `${DOCS_URL}/operations/applications/`;

const LOG_ROWS: { y: number; w: number; mark: string }[] = [
	{ y: 28, w: 48, mark: ART_SOFT },
	{ y: 39.5, w: 36, mark: ART_SOFT },
	{ y: 51, w: 54, mark: "var(--warn)" },
	{ y: 62.5, w: 30, mark: ART_SOFT },
	{ y: 74, w: 44, mark: "var(--err)" },
	{ y: 85.5, w: 38, mark: ART_SOFT },
];
const SPANS: { x: number; y: number; w: number; fill: string }[] = [
	{ x: 142, y: 30, w: 82, fill: "var(--info)" },
	{ x: 148, y: 42, w: 48, fill: ART_STRONG },
	{ x: 154, y: 54, w: 24, fill: ART_STRONG },
	{ x: 180, y: 54, w: 14, fill: ART_STRONG },
	{ x: 198, y: 66, w: 22, fill: ART_STRONG },
	{ x: 204, y: 78, w: 12, fill: ART_SOFT },
];
const EVENT_ROWS: { y: number; w: number; dot: string }[] = [
	{ y: 32, w: 50, dot: "var(--ok)" },
	{ y: 46, w: 40, dot: "var(--warn)" },
	{ y: 60, w: 56, dot: "var(--ok)" },
	{ y: 74, w: 34, dot: "var(--ok)" },
	{ y: 88, w: 46, dot: "var(--err)" },
];

function Panel({ view }: { view: ObservabilityView }) {
	switch (view) {
		case "logs":
			return LOG_ROWS.map((r) => (
				<g key={r.y}>
					<rect x={142} y={r.y} width={14} height={3} rx={1.5} fill={ART_SOFT} />
					<rect x={160} y={r.y} width={8} height={3} rx={1.5} fill={r.mark} />
					<rect x={172} y={r.y} width={r.w} height={3} rx={1.5} fill={ART_STRONG} />
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
					<rect x={154} y={r.y - 2} width={r.w} height={3.5} rx={1.75} fill={ART_STRONG} />
					<rect x={154} y={r.y + 4} width={r.w * 0.6} height={2.5} rx={1.25} fill={ART_SOFT} />
					<rect x={214} y={r.y - 2} width={12} height={3} rx={1.5} fill={ART_SOFT} />
				</g>
			));
	}
}

export function ObsArt({ view }: { view: ObservabilityView }) {
	return (
		<ClusterArt>
			<Panel view={view} />
		</ClusterArt>
	);
}

export function ObsHero({
	view,
	title,
	body,
	action,
	children,
}: {
	view: ObservabilityView;
	title: ReactNode;
	body: ReactNode;
	action?: ReactNode;
	children?: ReactNode;
}) {
	return (
		<EmptyHero art={<ObsArt view={view} />} title={title} subtitle={body}>
			{children}
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

/** True when the range already reaches as far back as this view can look, so an empty result means nothing was collected. */
export function coversWholeRange(range: TimeWindow, maxMs: number): boolean {
	return range.kind === "relative" && range.spanMs >= maxMs;
}

export function CopyCommand({ command }: { command: string }) {
	const [copied, copy] = useCopy();
	return (
		<Button
			variant="outline"
			aria-label={`Copy ${command}`}
			title="Copy"
			onClick={() => {
				copy("cmd", command, "Copied command");
			}}
			className="font-mono text-[12.5px]"
		>
			<span className="text-fg4">$</span>
			{command}
			{copied === null ? <CopyIcon className="text-fg3" /> : <CheckIcon className="text-ok-fg" />}
		</Button>
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

export function NoMatches({ title }: { title: string }) {
	const { tokens, text, setQuery } = useObs();
	return (
		<EmptyState
			icon={<SearchXIcon />}
			title={title}
			query={queryStr(tokens, text)}
			action={
				<>
					<Button
						variant="outline"
						onClick={() => {
							setQuery([], "");
						}}
					>
						<XIcon />
						Clear filters
					</Button>
					<WidenRangeButton />
				</>
			}
		/>
	);
}
