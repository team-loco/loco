import type { ReactNode } from "react";
import { TriangleAlertIcon } from "lucide-react";

import { EmptyState } from "@/components/design/EmptyState";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";

import { useObs } from "./context";
import { ObsPageEmpty, type ObsKind } from "./ObsEmpty";

function ObsSkeleton() {
	return (
		<div className="flex flex-col gap-4">
			<Skeleton className="h-[38px] w-full rounded-lg" />
			<Skeleton className="h-[130px] w-full rounded-lg" />
			<Skeleton className="h-[360px] w-full rounded-lg" />
		</div>
	);
}

export function ObsGate({ kind, children }: { kind: ObsKind; children: ReactNode }) {
	const { accessLoading, accessError, clusters, resources, resourcesLoading } = useObs();
	const needsTelemetry = kind !== "events";
	if (resourcesLoading) return <ObsSkeleton />;
	if (resources.length === 0) return <ObsPageEmpty kind={kind} />;
	if (needsTelemetry && accessLoading) return <ObsSkeleton />;
	if (needsTelemetry && accessError !== null) {
		return (
			<section className="rounded-lg border border-line">
				<EmptyState icon={<TriangleAlertIcon />} title="Couldn't reach observability">
					{getErrorMessage(accessError, "Failed to get observability access.")}
				</EmptyState>
			</section>
		);
	}
	if (needsTelemetry && clusters.length === 0) return <ObsPageEmpty kind={kind} />;
	return <>{children}</>;
}
