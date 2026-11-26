import type { ClusterCapacity } from "@gen/loco/cluster/v1/cluster_pb";

import { cn } from "@/lib/utils";

import { headroom, reservations } from "./capacity-model";
import { SystemTime } from "./shared";

/** Reservations explain deployment headroom; they are distinct from measured runtime usage. */
export function Capacity({
	capacity,
	reportLabel = "Reported",
}: {
	capacity: ClusterCapacity | undefined;
	reportLabel?: string;
}) {
	if (capacity === undefined) return <p className="m-0 px-5 py-5 text-[13.5px] text-fg3">Not reported</p>;
	return (
		<div data-slot="cluster-capacity" className="flex flex-col gap-4 px-5 py-4">
			<div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-fg3">
				<span className={capacity.stale ? "font-medium text-warn-fg" : ""}>
					{capacity.stale ? "Stale" : reportLabel}
				</span>
				<SystemTime ts={capacity.reportedAt} />
			</div>
			<div className="grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-3">
				{reservations(capacity).map((reservation) => {
					const over = reservation.requested > reservation.allocatable;
					const share =
						reservation.allocatable === 0 ? 0 : Math.min(100, (reservation.requested / reservation.allocatable) * 100);
					return (
						<div key={reservation.label} className="flex min-w-0 flex-col gap-2">
							<div className="flex items-baseline justify-between gap-2 text-[13px]">
								<span>{reservation.label}</span>
								<span className={cn("font-mono text-[12px]", over ? "text-warn-fg" : "text-fg2")}>
									{headroom(reservation)}
								</span>
							</div>
							<div aria-hidden="true" className="h-1 overflow-hidden rounded-sm bg-bg3">
								<div
									className={cn("h-full", over ? "bg-warn-fg" : "bg-fg3")}
									style={{ width: `${share.toString()}%` }}
								/>
							</div>
							<p className="m-0 font-mono text-[11.5px] text-fg3">
								{reservation.format(reservation.requested)}{" "}
								{reservation.label === "Pod slots" ? "assigned" : "requested"} /{" "}
								{reservation.format(reservation.allocatable)} allocatable
							</p>
						</div>
					);
				})}
			</div>
		</div>
	);
}

export function CapacityCell({ capacity }: { capacity: ClusterCapacity | undefined }) {
	if (capacity === undefined) return <span className="text-fg3">Not reported</span>;
	if (capacity.stale) return <span className="text-warn-fg">Stale report</span>;
	return (
		<div className="flex flex-col gap-0.5 font-mono text-[11.5px] text-fg2">
			{reservations(capacity)
				.slice(0, 2)
				.map((reservation) => (
					<span key={reservation.label}>{headroom(reservation)}</span>
				))}
		</div>
	);
}
