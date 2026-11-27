import type { AllocationCoverage } from "@gen/loco/capacity/v1/capacity_pb";

import { pluralize } from "@/lib/format";
import { formatDateTime, maybeTsMs, timeZoneLabel } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

export function ReservationCoverage({ coverage }: { coverage: AllocationCoverage | undefined }) {
	const pref = useTimePreferences();
	if (coverage === undefined) return null;
	const incomplete = coverage.missingClusters > 0 || coverage.staleClusters > 0;
	const at = maybeTsMs(coverage.oldestReportedAt);
	if (!incomplete && at === undefined) return null;
	return (
		<div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-2 text-[12px] text-fg3">
			{incomplete && <span className="text-warn-fg">Partial reservations</span>}
			{coverage.missingClusters > 0 && <span>{pluralize(coverage.missingClusters, "cluster")} not reporting</span>}
			{coverage.staleClusters > 0 && <span>{pluralize(coverage.staleClusters, "cluster")} stale</span>}
			{at !== undefined && (
				<span>
					Oldest report{" "}
					<time dateTime={new Date(at).toISOString()} className="font-mono">
						{formatDateTime(at, true, false, pref)} {timeZoneLabel(at, pref)}
					</time>
				</span>
			)}
		</div>
	);
}
