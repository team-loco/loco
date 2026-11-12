import { Server } from "lucide-react";
import { useState, useSyncExternalStore } from "react";

import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { cn } from "@/lib/utils";

import { demoFrame, DEPLOY_SHARE, ENDPOINT, REQUEST_SERIES, STRATEGY, TERMINAL, TONE_CLASS, type Pod } from "./deploy";
import { RolloutPlayer } from "./rolloutPlayer";

const REDUCED_MOTION = "(prefers-reduced-motion: reduce)";

function podClass(pod: Pod): string {
	switch (pod.phase) {
		case "ready":
			return "animate-pod-pop bg-ok-glyph";
		case "starting":
			return "animate-pod-starting border-[1.5px] border-warn motion-reduce:animate-none";
		case "terminating":
			return "bg-fg4 opacity-40";
	}
}

function Fact({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5 text-sm text-fg3">
			<span>{label}</span>
			<span className="truncate font-mono text-[12.5px] text-foreground">{value}</span>
		</div>
	);
}

const CHART_W = 300;
const CHART_H = 64;
const SERIES_MAX = Math.max(...REQUEST_SERIES) + 6;
const SERIES_LINE = `M${REQUEST_SERIES.map((v, i) => `${((i / (REQUEST_SERIES.length - 1)) * CHART_W).toFixed(1)},${(CHART_H - (v / SERIES_MAX) * 60).toFixed(1)}`).join(" L")}`;
const DEPLOY_X = CHART_W * DEPLOY_SHARE;

function RequestsChart({ rps, deployed }: { rps: number; deployed: boolean }) {
	return (
		<div className="flex flex-col gap-1.5">
			<div className="flex justify-between text-[12.5px] text-fg3">
				<span>Requests</span>
				<span className="font-mono text-foreground">{`${rps.toLocaleString()} req/s`}</span>
			</div>
			<svg
				viewBox={`0 0 ${String(CHART_W)} ${String(CHART_H)}`}
				preserveAspectRatio="none"
				aria-hidden="true"
				className="block h-16 w-full"
			>
				<path
					d={`${SERIES_LINE} L${String(CHART_W)},${String(CHART_H)} L0,${String(CHART_H)} Z`}
					fill="color-mix(in oklch, var(--brand) 12%, transparent)"
				/>
				<path d={SERIES_LINE} fill="none" stroke="var(--brand)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
				<line
					x1={DEPLOY_X}
					x2={DEPLOY_X}
					y1={0}
					y2={CHART_H}
					stroke="var(--fg4)"
					strokeDasharray="3 3"
					vectorEffect="non-scaling-stroke"
					className={cn("transition-opacity duration-300", deployed ? "opacity-100" : "opacity-0")}
				/>
			</svg>
		</div>
	);
}

export function DeployDemo() {
	const [{ subscribe, getSnapshot, observe }] = useState(
		() => new RolloutPlayer(window.matchMedia(REDUCED_MOTION).matches),
	);
	const frame = useSyncExternalStore(subscribe, getSnapshot);
	const { shown, status, ready, desired, regions, version, requestsPerSecond, p95Ms, deployed } = demoFrame(frame);

	return (
		<div
			id="product"
			ref={observe}
			className="scroll-mt-20 overflow-hidden rounded-[14px] border border-line2 bg-background shadow-[0_30px_60px_-30px_color-mix(in_oklch,var(--fg)_30%,transparent)]"
		>
			<div className="flex h-10 items-center gap-3.5 border-b border-line bg-panel px-3.5">
				<span aria-hidden="true" className="flex gap-1.5">
					<i className="size-2.5 rounded-full bg-line2" />
					<i className="size-2.5 rounded-full bg-line2" />
					<i className="size-2.5 rounded-full bg-line2" />
				</span>
				<span className="font-mono text-sm text-fg3">~/storefront/api</span>
			</div>
			<div className="grid min-[860px]:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
				<div className="min-w-0 px-5 py-4 font-mono text-[12.5px] leading-[1.8]">
					{TERMINAL.map((line, i) => {
						const visible = i < shown;
						return (
							<div
								key={line.text}
								className={cn(
									"truncate whitespace-pre",
									TONE_CLASS[line.tone],
									visible ? "animate-log-in motion-reduce:animate-none" : "invisible",
								)}
							>
								{line.text}
								{i === shown - 1 && (
									<span
										aria-hidden="true"
										className="ml-1.5 inline-block h-3.5 w-[7px] translate-y-0.5 animate-pulse-soft bg-fg3 motion-reduce:animate-none"
									/>
								)}
							</div>
						);
					})}
				</div>
				<div className="flex min-w-0 flex-col gap-3.5 border-line bg-panel px-5 py-[18px] max-[859px]:border-t min-[860px]:border-l">
					<div className="flex items-center gap-2">
						<Server className="size-[15px] text-fg2" aria-hidden="true" />
						<span className="text-[14.5px] font-semibold">api</span>
						<span className="rounded-[4px] bg-panel2 px-[7px] py-px text-xs text-fg2">production</span>
						<span className="ml-auto truncate font-mono text-xs text-fg3 transition-colors duration-300">
							{version}
						</span>
					</div>
					<div className="flex items-center gap-2.5">
						<ResourceStatusBadge status={status} />
						<span className="font-mono text-sm text-fg3">
							{ready}/{desired} ready
						</span>
					</div>
					<div className="flex flex-col gap-2 rounded-[9px] border border-line bg-background p-3">
						{regions.map((region) => (
							<div key={region.name} className="flex items-center gap-3">
								<span className="w-[72px] shrink-0 font-mono text-sm text-fg3">{region.name}</span>
								<span aria-hidden="true" className="flex h-4 gap-1.5">
									{region.pods.map((pod) => (
										<i key={pod.id} className={cn("size-4 rounded-[4px] transition-opacity", podClass(pod))} />
									))}
								</span>
							</div>
						))}
					</div>
					<RequestsChart rps={requestsPerSecond} deployed={deployed} />
					<div className="mt-auto grid grid-cols-2 gap-3 border-t border-line pt-3">
						<Fact label="Endpoint" value={ENDPOINT} />
						<Fact label="p95" value={`${String(p95Ms)} ms`} />
						<div className="col-span-2">
							<Fact label="Strategy" value={STRATEGY} />
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
