import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { cn } from "@/lib/utils";
import { isInFlight } from "@/lib/deployment-utils";

import { shortId } from "./format";
import { depTag, type RegionView } from "./model";
import { rolloutSteps, type RolloutStep } from "./rollout";

function barClass(step: RolloutStep): string {
	switch (step.state) {
		case "done":
			return "bg-ok-glyph";
		case "active":
			return "bg-info motion-safe:animate-pulse-soft";
		case "failed":
			return "bg-err";
		case "todo":
			return "bg-line2";
	}
}

function RegionRollout({ region, phaseOf }: { region: RegionView; phaseOf: ReturnType<typeof useDeploymentWatch> }) {
	const dep = region.current;
	if (dep === undefined) return null;
	const watched = phaseOf(dep.id);
	const phase = watched.phase ?? dep.status;
	const message = watched.phase === undefined ? dep.message : watched.message;
	const steps = rolloutSteps(phase, message, region.status?.readyReplicas ?? 0, region.status?.desiredReplicas ?? 0);
	const titleId = `rollout-${region.name}`;
	return (
		<section aria-labelledby={titleId} className="flex flex-col gap-3.5 rounded-card bg-panel px-5 py-[18px]">
			<div className="flex flex-wrap items-baseline gap-2.5">
				<h2 id={titleId} className="m-0 font-mono text-[15px] font-medium">
					Rolling out {depTag(dep)} in {region.name}
				</h2>
				<span className="font-mono text-[12px] text-fg3">{shortId(dep.id)}</span>
			</div>
			<ol aria-label="Rollout progress" className="m-0 grid list-none grid-cols-2 gap-1.5 p-0 sm:grid-cols-4">
				{steps.map((s) => (
					<li key={s.label} className={cn("flex min-w-0 flex-col gap-1", s.state === "todo" && "text-fg3")}>
						<span aria-hidden className={cn("mb-1.5 h-[3px] rounded-[2px]", barClass(s))} />
						<span className="text-[13px]">
							{s.label}
							<span className="sr-only">: {s.state}</span>
						</span>
						<span className="truncate font-mono text-[11.5px] text-fg3" title={s.detail}>
							{s.detail}
						</span>
					</li>
				))}
			</ol>
		</section>
	);
}

export function RolloutProgress({ regions }: { regions: RegionView[] }) {
	const rolling = regions.filter((r) => r.current !== undefined && isInFlight(r.current.status));
	const phaseOf = useDeploymentWatch(rolling.flatMap((r) => (r.current === undefined ? [] : [r.current.id])));
	return (
		<>
			{rolling.map((r) => (
				<RegionRollout key={r.name} region={r} phaseOf={phaseOf} />
			))}
		</>
	);
}
