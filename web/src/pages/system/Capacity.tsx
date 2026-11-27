import { reservationQuantity } from "@/lib/reservation";
import { cn } from "@/lib/utils";
import { Skeleton } from "@/components/design/Skeleton";
import { headroom, reservations, type CapacitySummary } from "./capacity-model";
import { SystemTime } from "./shared";

export function Capacity({ summary }: { summary: CapacitySummary | undefined }) {
	if (summary === undefined) return <p className="m-0 px-5 py-5 text-[13px] text-fg3">Not reported</p>;
	const stale = summary.capacityStale || (summary.sample?.stale ?? false);
	const rows = reservations(summary);
	return (
		<div className="flex flex-col gap-4 px-5 py-4" data-slot="cluster-capacity">
			<div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-fg3">
				<span className={summary.sample?.stale ? "text-warn-fg" : ""}>
					{summary.sample?.stale ? "Reservations stale" : "Reservations"}
				</span>
				<SystemTime ts={summary.sample?.reportedAt} />
				<span className={summary.capacityStale ? "text-warn-fg" : ""}>
					{summary.capacityStale ? "Capacity stale" : "Capacity"}
				</span>
				<SystemTime ts={summary.capacityReportedAt} />
			</div>
			<div className="grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-2">
				{rows.map((row) => {
					let label = headroom(row);
					if (stale) label = "Stale";
					const requested = row.requested;
					const allocatable = row.allocatable;
					const comparable = requested !== undefined && allocatable !== undefined;
					const over = comparable && requested > allocatable;
					let share = 0;
					if (requested !== undefined && allocatable !== undefined) {
						if (allocatable > 0n) share = Math.min(100, (Number(requested) / Number(allocatable)) * 100);
					}

					return (
						<div key={row.label} className="flex min-w-0 flex-col gap-2">
							<div className="flex items-baseline justify-between gap-2 text-[13px]">
								<span>{row.label}</span>
								<span className={cn("font-mono text-[12px]", over ? "text-warn-fg" : "text-fg2")}>{label}</span>
							</div>
							{requested !== undefined && allocatable !== undefined && (
								<div aria-hidden="true" className="h-1 overflow-hidden rounded-sm bg-bg3">
									<div className={cn("h-full", over ? "bg-warn-fg" : "bg-fg3")} style={{ width: `${share}%` }} />
								</div>
							)}
							<p className="m-0 font-mono text-[11.5px] text-fg3">
								{requested !== undefined
									? `${reservationQuantity(requested, row.memory)} requested`
									: "Requests not reported"}{" "}
								/{" "}
								{row.allocatable === undefined
									? "Capacity not reported"
									: `${reservationQuantity(row.allocatable, row.memory)} allocatable`}
							</p>
						</div>
					);
				})}
			</div>
		</div>
	);
}

export function CapacityCell({
	summary,
	loading = false,
}: {
	summary: CapacitySummary | undefined;
	loading?: boolean;
}) {
	if (loading)
		return (
			<div className="flex flex-col gap-1">
				<Skeleton className="h-3 w-28" />
				<Skeleton className="h-3 w-28" />
			</div>
		);
	if (summary?.sample === undefined) return <span className="text-fg3">Not reported</span>;
	if (summary.sample.stale || summary.capacityStale) return <span className="text-warn-fg">Stale report</span>;
	return (
		<div className="flex flex-col gap-0.5 font-mono text-[11.5px] text-fg2">
			{reservations(summary).map((row) => (
				<span key={row.label}>{headroom(row)}</span>
			))}
		</div>
	);
}
