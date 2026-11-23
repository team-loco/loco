import type { DeploymentPlacement } from "@gen/loco/deployment/v1/deployment_pb";
import type { ReactNode } from "react";

import { placementPhase } from "@/lib/deployment-utils";
import { cn } from "@/lib/utils";

import { deploymentPhaseStyle } from "./StatusBadge";
import { StatusGlyph } from "./StatusGlyph";

/** A region code in a small chip. */
export function RegionChip({ title, children }: { title?: string; children: ReactNode }) {
	return (
		<span
			title={title}
			className="dwa-chip inline-flex items-center gap-1 rounded-[4px] bg-background px-[7px] py-0.5 font-mono text-[11.5px] text-fg2 shadow-[inset_0_0_0_1px_var(--line)]"
		>
			{children}
		</span>
	);
}

/** One chip per region of a deployment: its state glyph and its name. */
export function PlacementChips({
	placements,
	className,
}: {
	placements: readonly DeploymentPlacement[];
	className?: string;
}) {
	return (
		<div className={cn("flex flex-wrap gap-1", className)}>
			{placements.map((placement) => {
				const style = deploymentPhaseStyle(placementPhase(placement.status));
				return (
					<RegionChip key={placement.region} title={`${placement.region}: ${style.label}`}>
						<StatusGlyph glyph={style.glyph} className="size-3" />
						{placement.region}
						<span className="sr-only">, {style.label}</span>
					</RegionChip>
				);
			})}
		</div>
	);
}
