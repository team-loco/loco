import { useStreamEvents } from "@/hooks/useStreamEvents";
import { cn } from "@/lib/utils";
import { useState } from "react";

export function ActivitySheet({
	open,
	onClose,
	resourceId,
}: {
	open: boolean;
	onClose: () => void;
	resourceId: string;
}) {
	const { events } = useStreamEvents(resourceId);
	const [filter, setFilter] = useState<"all" | "warning" | "normal">("all");

	const filtered =
		filter === "all"
			? events
			: filter === "warning"
				? events.filter((e) => e.severity === "Warning")
				: events.filter((e) => e.severity === "Normal");

	const filters = [
		{ id: "all" as const, label: "All" },
		{ id: "warning" as const, label: "Warnings" },
		{ id: "normal" as const, label: "Normal" },
	];

	return (
		<>
			<div
				className={cn(
					"fixed inset-0 bg-[rgba(42,32,24,0.12)] z-40 transition-opacity duration-280 ease-in",
					open
						? "opacity-100 pointer-events-auto"
						: "opacity-0 pointer-events-none",
				)}
				onClick={onClose}
			/>
			<div
				className={cn(
					"fixed top-0 right-0 bottom-0 w-[min(420px,100vw)] bg-card border-l border-[#e8e0d4] z-50 flex flex-col shadow-[-8px_0_40px_rgba(42,32,24,0.12)] transition-transform duration-[320ms] ease-[cubic-bezier(0.32,0.72,0,1)]",
					open ? "translate-x-0" : "translate-x-full",
				)}
			>
				<div className="px-[22px] pt-5 border-b border-[#e8e0d4]">
					<div className="flex items-center justify-between mb-3.5">
						<span className="font-serif text-[18px]">Activity</span>
						<button
							onClick={onClose}
							className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 rounded-sm"
						>
							<svg
								width="17"
								height="17"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<line x1="18" y1="6" x2="6" y2="18" />
								<line x1="6" y1="6" x2="18" y2="18" />
							</svg>
						</button>
					</div>
					<div className="flex gap-0.5 -mb-px">
						{filters.map((f) => (
							<button
								key={f.id}
								onClick={() => {
									setFilter(f.id);
								}}
								className={cn(
									"bg-transparent border-none cursor-pointer px-3 py-[7px] text-xs font-medium font-sans transition-all duration-[140ms] border-b-2",
									filter === f.id
										? "text-foreground border-[#c4956a]"
										: "text-[#8a7a68] border-transparent",
								)}
							>
								{f.label}
							</button>
						))}
					</div>
				</div>
				<div className="flex-1 overflow-y-auto px-[22px] py-4">
					{filtered.length === 0 ? (
						<div className="text-center py-10 text-[#a0907e] text-[13px]">
							No events yet
						</div>
					) : (
						<div className="flex flex-col gap-0.5">
							{filtered.map((ev, i) => {
								const warn = ev.severity === "Warning";
								const cfg = warn
									? {
											icon: "!",
											bg: "#fdf3e3",
											color: "#9c6b1e",
											dot: "#d4870a",
										}
									: {
											icon: "✓",
											bg: "#eaf2ed",
											color: "#3a6b4a",
											dot: "#4a7c59",
										};
								return (
									<div
										key={i}
										className="flex gap-3 p-[10px_12px] rounded-md transition-colors duration-120 hover:bg-[#f2ece2]"
									>
										<div
											className="w-7 h-7 rounded-lg shrink-0 flex items-center justify-center text-xs font-bold mt-px"
											style={{
												background: cfg.bg,
												border: `1px solid ${cfg.dot}30`,
												color: cfg.color,
											}}
										>
											{cfg.icon}
										</div>
										<div className="flex-1 min-w-0">
											<div className="text-[13px] font-medium text-foreground mb-0.5 leading-[1.3]">
												{ev.eventType}
											</div>
											<div className="text-[11px] text-[#8a7a68] mb-[5px] leading-[1.4]">
												{ev.message}
											</div>
											<div className="flex items-center gap-2">
												<span className="text-[10px] text-[#b0a090] font-mono">
													{new Date(ev.timestamp).toLocaleTimeString()}
												</span>
												{ev.pod && (
													<span className="text-[10px] bg-[#ede7dd] text-[#8a7a68] px-[6px] py-px rounded font-mono">
														{ev.pod}
													</span>
												)}
											</div>
										</div>
									</div>
								);
							})}
						</div>
					)}
				</div>
			</div>
		</>
	);
}

// ─── Settings Sheet ───────────────────────────────────────────────────────────
