import type { ReactNode } from "react";
import { RadarIcon, TriangleAlertIcon } from "lucide-react";

import { EmptyState } from "@/components/design/EmptyState";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";

import { useObs } from "./context";

export function ObsGate({ children }: { children: ReactNode }) {
	const { accessLoading, accessError, clusters } = useObs();
	if (accessLoading) {
		return (
			<div className="flex flex-col gap-4">
				<Skeleton className="h-[130px] w-full rounded-lg" />
				<Skeleton className="h-[360px] w-full rounded-lg" />
			</div>
		);
	}
	if (accessError !== null) {
		return (
			<section className="rounded-lg border border-line">
				<EmptyState icon={<TriangleAlertIcon />} title="Couldn't reach observability">
					{getErrorMessage(accessError, "Failed to get observability access.")}
				</EmptyState>
			</section>
		);
	}
	if (clusters.length === 0) {
		return (
			<section className="rounded-lg border border-dashed border-line2">
				<EmptyState icon={<RadarIcon />} title="No telemetry yet">
					This workspace has no clusters collecting logs or metrics. Deploy a resource and its telemetry shows up here.
				</EmptyState>
			</section>
		);
	}
	return <>{children}</>;
}
