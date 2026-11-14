import { CpuIcon, KeyRoundIcon, ServerIcon } from "lucide-react";
import { Fragment } from "react";

import { Skeleton } from "@/components/design/Skeleton";
import { cn } from "@/lib/utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { dayLabel, shortAgo, type AuditRow } from "./model";

const GRID = "grid grid-cols-[110px_200px_minmax(300px,1fr)_220px] items-center gap-x-5";
const SKELETON_ROWS = [0, 1, 2, 3, 4, 5];
const TOKEN_ACTORS = new Set(["organization", "workspace", "resource"]);

function ActorBadge({ row }: { row: AuditRow }) {
	const { actorType, actorEmail } = row.event;
	if (actorType === "user") {
		return (
			<MemberAvatar
				name={row.actor.name}
				email={actorEmail}
				avatarUrl=""
				className="size-6 [&_[data-slot=avatar-fallback]]:text-[10px]"
			/>
		);
	}
	const Icon = TOKEN_ACTORS.has(actorType) ? KeyRoundIcon : actorType === "agent" ? ServerIcon : CpuIcon;
	return (
		<span className="flex size-6 shrink-0 items-center justify-center rounded-[6px] border border-line bg-background text-fg2">
			<Icon className="size-3" />
		</span>
	);
}

export function AuditTable({
	rows,
	loading,
	selectedId,
	nowMs,
	onSelect,
}: {
	rows: readonly AuditRow[];
	loading: boolean;
	selectedId: string | null;
	nowMs: number;
	onSelect: (id: string) => void;
}) {
	return (
		<div className="overflow-x-auto px-5 [scrollbar-width:thin]">
			<div className="min-w-[890px]">
				<div className={cn(GRID, "h-[38px] border-b border-line2 text-[12.5px] text-fg3")}>
					<span>Time</span>
					<span>Actor</span>
					<span>Action</span>
					<span>Where</span>
				</div>
				{loading &&
					SKELETON_ROWS.map((i) => (
						<div key={i} className="flex h-12 items-center border-b border-line2">
							<Skeleton className="h-3 w-full" />
						</div>
					))}
				{!loading && (
					<ul aria-label="Audit events" className="m-0 list-none p-0">
						{rows.map((row, i) => {
							const day = dayLabel(row.ms, nowMs);
							const selected = row.event.id === selectedId;
							return (
								<Fragment key={row.event.id}>
									{(i === 0 || dayLabel(rows[i - 1]?.ms ?? 0, nowMs) !== day) && (
										<li className="sticky left-0 pt-4 pb-1.5 text-[12px] text-fg3">{day}</li>
									)}
									<li>
										<button
											type="button"
											aria-pressed={selected}
											onClick={() => {
												onSelect(row.event.id);
											}}
											className={cn(
												GRID,
												"min-h-12 w-full border-b border-line2 py-1.5 text-left text-[13px] hover:bg-[color-mix(in_oklch,var(--panel2)_60%,transparent)] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
												selected && "rounded-[6px] bg-background shadow-[inset_0_0_0_1px_var(--line2)]",
											)}
										>
											<span className="font-mono text-[12px] text-fg2" title={`${shortAgo(row.ms, nowMs)} ago`}>
												{new Date(row.ms).toLocaleTimeString([], {
													hour: "numeric",
													minute: "2-digit",
													second: "2-digit",
												})}
											</span>
											<span className="flex min-w-0 items-center gap-[9px]">
												<ActorBadge row={row} />
												<span className="flex min-w-0 flex-col leading-[1.3]">
													<span className="truncate">{row.actor.name}</span>
													{row.actor.detail !== "" && (
														<span className="truncate text-[11.5px] text-fg3">{row.actor.detail}</span>
													)}
												</span>
											</span>
											<span className="flex min-w-0 items-baseline gap-2">
												<span className="shrink-0">{row.label}</span>
												<span className="min-w-0 truncate font-mono text-[12px] text-fg2">{row.detail}</span>
											</span>
											<span className="flex min-w-0 items-center gap-2 font-mono text-[12px] text-fg2">
												<span className="truncate">{row.where}</span>
												{row.environment !== "" && (
													<span className="shrink-0 rounded-[5px] border border-line px-1.5 text-[11px] text-fg3">
														{row.environment}
													</span>
												)}
											</span>
										</button>
									</li>
								</Fragment>
							);
						})}
					</ul>
				)}
			</div>
		</div>
	);
}
