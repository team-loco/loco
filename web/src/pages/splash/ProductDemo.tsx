import { useState, useSyncExternalStore } from "react";

import { cn } from "@/lib/utils";

import { ClusterDiagram, ClusterDiagramStacked } from "./ClusterDiagram";
import { readyCount, type LogTone } from "./cluster";
import { Frame } from "./primitives";
import { RolloutPlayer, type LogLine } from "./rolloutPlayer";

const LOG_TONE: Record<LogTone, string> = {
	ink: "text-foreground",
	body: "text-fg2",
	ok: "text-ok-fg",
	muted: "text-fg3",
	accent: "text-primary",
};

function Fact({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5 border-t border-l border-line px-4 py-3">
			<span className="text-sm text-fg3">{label}</span>
			<span className="truncate font-mono text-[13px]">{value}</span>
		</div>
	);
}

function RolloutLog({ lines }: { lines: readonly LogLine[] }) {
	return (
		<div className="h-[104px] overflow-hidden border-t border-line bg-bg2 px-4 py-2.5 font-mono text-sm leading-[1.7]">
			{lines.map((line) => (
				<div key={line.id} className="flex animate-log-in gap-3 whitespace-pre">
					<span className="text-fg4">{line.time}</span>
					<span className={cn("truncate", LOG_TONE[line.tone])}>{line.msg}</span>
				</div>
			))}
		</div>
	);
}

export function ProductDemo() {
	const [{ subscribe, getSnapshot, observe }] = useState(() => new RolloutPlayer());
	const { rollout, log } = useSyncExternalStore(subscribe, getSnapshot);
	const ready = readyCount(rollout);
	const { rolling, target, tag } = rollout;

	return (
		<section id="product" ref={observe} className="scroll-mt-16">
			<Frame className="pt-2 pb-[120px]">
				<div className="overflow-hidden rounded-[14px] border border-line bg-background shadow-[0_12px_32px_-18px_rgba(0,0,0,0.22)]">
					<div className="flex min-h-12 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line py-2 pr-3.5 pl-4">
						<span className="flex items-center gap-2 text-[13.5px] font-medium">
							<span className="text-fg3">team-loco</span>
							<span className="text-fg4">/</span>
							storefront
							<span className="text-fg4">/</span>
							<span className="flex items-center gap-1.5">
								<span className="size-1.5 rounded-full bg-ok-fg" />
								production
							</span>
						</span>
						<div className="flex-1" />
						<span className={cn("font-mono text-sm", rolling ? "text-warn" : "text-ok-fg")}>
							{rolling ? `rolling update · ${ready}/${target} on sha-${tag}` : "all replicas healthy"}
						</span>
						<span className="flex items-center gap-1.5 font-mono text-sm text-fg3">
							<span className="size-1.5 animate-pulse-soft rounded-full bg-ok-fg" />
							live
						</span>
					</div>
					<div className="hidden overflow-x-auto min-[780px]:block">
						<div className="min-w-[900px]">
							<ClusterDiagram rollout={rollout} />
						</div>
					</div>
					<div className="min-[780px]:hidden">
						<ClusterDiagramStacked rollout={rollout} />
					</div>
					<RolloutLog lines={log} />
					<div className="overflow-hidden border-t border-line">
						<div className="-mt-px -ml-px grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))]">
							<Fact label="Endpoint" value="https://api.onloco.app" />
							<Fact label="Image" value={`api:sha-${tag}`} />
							<Fact label="Strategy" value="rolling · surge 1" />
							<Fact label="Ready" value={`${ready} of ${target}`} />
						</div>
					</div>
				</div>
			</Frame>
		</section>
	);
}
