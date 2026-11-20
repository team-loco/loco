import type { DeployMarker } from "@/lib/deployMarkers";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { cn } from "@/lib/utils";

const DENSE_AFTER = 5;
const BASE_DOT_PX = 5;
const MAX_DOT_PX = 8;

/** Deployment dots along the bottom of a sparkline; place it inside a relatively positioned box. */
export function DeployMarkers({ markers }: { markers: DeployMarker[] }) {
	const dense = markers.length > DENSE_AFTER;
	return (
		<>
			{markers.map((marker) => {
				const dot = Math.min(MAX_DOT_PX, BASE_DOT_PX + Math.log2(marker.count));
				return (
					<Tooltip key={marker.at}>
						<TooltipTrigger
							render={<span />}
							tabIndex={0}
							aria-label={marker.tip.replace("\n", ", ")}
							data-slot="deploy-marker"
							data-failed={marker.failed ? "" : undefined}
							className={cn(
								"group/mk absolute -top-[3px] -bottom-1 z-[2] w-3 -translate-x-1/2 outline-none",
								"before:absolute before:top-0 before:bottom-[3px] before:left-1/2 before:border-l before:border-dashed before:border-fg4 before:opacity-55 before:transition-opacity",
								dense ? "before:opacity-0" : "",
								"hover:before:opacity-100 focus-visible:before:opacity-100",
							)}
							style={{ left: `${(marker.at * 100).toFixed(2)}%` }}
						>
							<span
								aria-hidden="true"
								className={cn(
									"absolute bottom-0 left-1/2 -translate-x-1/2 rounded-full shadow-[0_0_0_2px_var(--card-bg,var(--panel))]",
									marker.failed ? "bg-bad-fg" : "bg-fg3",
								)}
								style={{ width: `${dot.toFixed(1)}px`, height: `${dot.toFixed(1)}px` }}
							/>
						</TooltipTrigger>
						<TooltipContent className="font-mono text-[11.5px] whitespace-pre-line">{marker.tip}</TooltipContent>
					</Tooltip>
				);
			})}
		</>
	);
}
