import { useTimePreferences } from "@/lib/use-time-preferences";
import { CpuIcon, KeyRoundIcon, ServerIcon } from "lucide-react";
import { Fragment, useState } from "react";

import { ColumnResizeHandle } from "@/components/design/ColumnResizeHandle";
import { Skeleton } from "@/components/design/Skeleton";
import { agoLabel, formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { dayLabel, type AuditRow } from "./model";

const GRID = "grid items-center gap-x-5";
const HEADINGS = ["Time", "Actor", "Action", "Where"] as const;
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
	highlightAfter,
	loading,
	selectedId,
	nowMs,
	onSelect,
}: {
	rows: readonly AuditRow[];
	highlightAfter?: bigint | undefined;
	loading: boolean;
	selectedId: string | null;
	nowMs: number;
	onSelect: (id: string) => void;
}) {
	const timePref = useTimePreferences();
	const [widths, setWidths] = useState([110, 200, 300, 220]);
	const gridStyle = {
		gridTemplateColumns: widths
			.map((width, index) => (index === 2 ? `minmax(${width}px,1fr)` : `${width}px`))
			.join(" "),
	};
	const minWidth = widths.reduce((sum, width) => sum + width, 100);

	return (
		<div className="overflow-x-auto [scrollbar-width:thin]">
			<div style={{ minWidth }}>
				<div
					style={gridStyle}
					data-slot="table-header"
					className={cn(GRID, "h-10 border-b border-line2 px-5 text-[12.5px] text-fg3")}
				>
					{HEADINGS.map((label, index) => (
						<span key={label} className="relative flex h-full items-center">
							{label}
							<ColumnResizeHandle
								label={label}
								width={widths[index] ?? 90}
								min={90}
								max={640}
								onWidth={(width) => {
									setWidths((current) => current.map((value, i) => (i === index ? width : value)));
								}}
							/>
						</span>
					))}
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
							const day = dayLabel(row.ms, nowMs, timePref);
							const selected = row.event.id === selectedId;
							return (
								<Fragment key={row.event.id}>
									{(i === 0 || dayLabel(rows[i - 1]?.ms ?? 0, nowMs, timePref) !== day) && (
										<li className="sticky left-0 px-5 pt-4 pb-1.5 text-[12px] text-fg3">{day}</li>
									)}
									<li>
										<button
											type="button"
											style={gridStyle}
											aria-pressed={selected}
											onClick={() => {
												onSelect(row.event.id);
											}}
											className={cn(
												GRID,
												"min-h-[52px] w-full border-b border-line2 px-5 py-2.5 text-left text-[13px] hover:bg-(--row-hover) focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
												(i === rows.length - 1 || dayLabel(rows[i + 1]?.ms ?? 0, nowMs, timePref) !== day) &&
													"border-b-0",
												highlightAfter !== undefined &&
													row.event.seq > highlightAfter &&
													"motion-safe:animate-[audit-event-in_2s_ease-out]",
												selected && "bg-background shadow-[inset_0_0_0_1px_var(--line2)]",
											)}
										>
											<span className="font-mono text-[12px] text-fg2" title={agoLabel(row.ms, nowMs)}>
												{formatClock(row.ms, false, timePref)}
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
