import { useId, type CSSProperties, type ReactNode } from "react";
import { ActivityIcon, ChartLineIcon, HistoryIcon, RocketIcon, ScrollTextIcon } from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useCopy } from "@/hooks/useCopy";
import type { TimeRange } from "@/lib/obs";
import { workspacePath } from "@/lib/routes";

import { useObs } from "./context";
import { CopyButton } from "./shared";

export type ObsKind = "logs" | "metrics" | "events";

const DEPLOY_COMMAND = "loco deploy <name>";
const COPY_RESET_MS = 1400;

const ART_W = 576;
const ART_H = 168;
const AXIS_Y = 150;
const X0 = 52;
const X1 = ART_W - 16;
const PLOT_TOP = 30;
const SWEEP_TOP = 14;
const SWEEP_TAIL = 40;
const TICKS = 24;
const MAJOR_TICK_EVERY = 6;
const MAJOR_TICK = 6;
const MINOR_TICK = 3;
const LABEL_Y = 166;
const NOW_LABEL_OFFSET = 22;
const LOG_BARS = 48;
const EVENT_BARS = 24;
const BAR_H = 3;

const KIND_TITLE: Record<ObsKind, string> = { logs: "Logs", metrics: "Metrics", events: "Events" };

const KIND_AXIS: Record<ObsKind, { unit: string; ticks: string[] }> = {
	logs: { unit: "lines", ticks: ["100", "200", "300"] },
	metrics: { unit: "req/s", ticks: ["25", "50", "75"] },
	events: { unit: "events", ticks: ["5", "10", "15"] },
};

function kindIcon(kind: ObsKind): ReactNode {
	switch (kind) {
		case "logs":
			return <ScrollTextIcon className="size-3.5" />;
		case "metrics":
			return <ChartLineIcon className="size-3.5" />;
		case "events":
			return <ActivityIcon className="size-3.5" />;
	}
}

function ArtText({ x, y, anchor, children }: { x: number; y: number; anchor?: "end"; children: string }) {
	return (
		<text x={x} y={y} textAnchor={anchor} fontSize={10} className="fill-fg3 font-mono">
			{children}
		</text>
	);
}

function Bars({ count }: { count: number }) {
	const bw = (X1 - X0) / count;
	return Array.from({ length: count }, (_, i) => (
		<rect key={String(i)} x={X0 + i * bw + 1} y={AXIS_Y - BAR_H} width={bw - 2} height={BAR_H} rx={1} className="fill-pe-bar" />
	));
}

function EmptyArt({ kind, range }: { kind: ObsKind; range: TimeRange }) {
	const fadeId = useId();
	const { unit, ticks } = KIND_AXIS[kind];
	const step = (AXIS_Y - PLOT_TOP) / ticks.length;
	const sweepStyle = { "--sw": `${String(X1 - X0)}px`, animation: "pe-sweep 4.5s linear infinite" } as CSSProperties;
	return (
		<svg width="100%" viewBox={`0 0 ${String(ART_W)} ${String(ART_H)}`} className="pe-art block overflow-visible" aria-hidden>
			<defs>
				<linearGradient id={fadeId} x1={0} x2={1}>
					<stop offset={0} stopColor="var(--accent)" stopOpacity={0} />
					<stop offset={1} stopColor="var(--accent)" style={{ stopOpacity: "var(--pe-glow)" }} />
				</linearGradient>
			</defs>
			<ArtText x={X0 - 8} y={12} anchor="end">
				{unit}
			</ArtText>
			{ticks.map((t, i) => {
				const y = AXIS_Y - (i + 1) * step;
				return (
					<g key={t}>
						<line x1={X0} x2={X1} y1={y} y2={y} strokeDasharray="2 4" className="stroke-pe-grid" />
						<ArtText x={X0 - 8} y={y + 3} anchor="end">
							{t}
						</ArtText>
					</g>
				);
			})}
			<ArtText x={X0 - 8} y={AXIS_Y + 3} anchor="end">
				0
			</ArtText>
			{kind === "metrics" ? (
				<line x1={X0} x2={X1} y1={AXIS_Y} y2={AXIS_Y} strokeWidth={2} className="stroke-primary" />
			) : (
				<Bars count={kind === "logs" ? LOG_BARS : EVENT_BARS} />
			)}
			<line x1={X0} x2={X1} y1={AXIS_Y} y2={AXIS_Y} className="stroke-pe-bar" />
			{Array.from({ length: TICKS + 1 }, (_, i) => {
				const x = X0 + i * ((X1 - X0) / TICKS);
				const h = i % MAJOR_TICK_EVERY === 0 ? MAJOR_TICK : MINOR_TICK;
				return <line key={String(i)} x1={x} x2={x} y1={AXIS_Y - h} y2={AXIS_Y + h} className="stroke-pe-bar" />;
			})}
			<ArtText x={X0} y={LABEL_Y}>{`-${range}`}</ArtText>
			<ArtText x={X1 - NOW_LABEL_OFFSET} y={LABEL_Y}>
				now
			</ArtText>
			<circle cx={X1} cy={AXIS_Y} r={4} className="fill-primary" />
			<circle cx={X1} cy={AXIS_Y} r={4} className="origin-center animate-ping-soft fill-primary [transform-box:fill-box]" />
			<g style={sweepStyle}>
				<line x1={X0} x2={X0} y1={SWEEP_TOP} y2={AXIS_Y} strokeWidth={1} opacity={0.55} className="stroke-primary" />
				<rect x={X0 - SWEEP_TAIL} y={SWEEP_TOP} width={SWEEP_TAIL} height={AXIS_Y - SWEEP_TOP} fill={`url(#${fadeId})`} />
			</g>
		</svg>
	);
}

function Listening() {
	return (
		<span className="flex items-center gap-1.5 text-sm text-fg3">
			<span className="relative size-[7px]">
				<span className="absolute inset-0 animate-ping-soft rounded-full bg-primary motion-reduce:animate-none" />
				<span className="absolute inset-0 rounded-full bg-primary" />
			</span>
			listening
		</span>
	);
}

function DeployCommand() {
	const [copied, copy] = useCopy(COPY_RESET_MS);
	return (
		<div className="flex h-[38px] items-center gap-2.5 rounded-lg border border-line bg-bg2 pr-1.5 pl-3.5 font-mono">
			<span className="text-fg4">$</span>
			<span>{DEPLOY_COMMAND}</span>
			<CopyButton
				copied={copied !== null}
				onCopy={() => {
					copy(DEPLOY_COMMAND, DEPLOY_COMMAND);
				}}
				className="size-7"
			/>
		</div>
	);
}

export function ObsPageEmpty({ kind }: { kind: ObsKind }) {
	const { range } = useObs();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const workspace = workspaces.find((w) => w.id === activeWorkspaceId);
	const dashboard = activeOrgId !== null && activeWorkspaceId !== null ? workspacePath(activeOrgId, activeWorkspaceId) : "/";
	const art = (
		<div className="w-full max-w-[600px] overflow-hidden rounded-lg border border-line2 bg-pe-panel text-left shadow-pe">
			<div className="flex h-[34px] items-center gap-2 border-b border-line bg-pe-head px-3 text-sm text-fg2">
				<span className="flex text-fg3">{kindIcon(kind)}</span>
				<span className="font-medium text-foreground">{`${KIND_TITLE[kind]} · last ${range}`}</span>
				<div className="flex-1" />
				<Listening />
			</div>
			<div className="px-3 pt-4 pb-3">
				<EmptyArt kind={kind} range={range} />
			</div>
		</div>
	);
	return (
		<EmptyHero
			art={art}
			title={`No ${kind} yet`}
			subtitle={`${workspace?.name ?? "This workspace"} has no running services`}
			className="mt-2"
		>
			<DeployCommand />
			<Button size="lg" nativeButton={false} render={<Link to={dashboard} />} className="px-3.5">
				<RocketIcon />
				Deploy a service
			</Button>
		</EmptyHero>
	);
}

export function WidenRangeButton({ max }: { max: TimeRange }) {
	const { setRange } = useObs();
	return (
		<Button
			variant="outline"
			onClick={() => {
				setRange(max);
			}}
		>
			<HistoryIcon />
			{`Show last ${max}`}
		</Button>
	);
}
