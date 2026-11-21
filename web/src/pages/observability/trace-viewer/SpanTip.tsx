import type { ReactElement, ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger, createTooltipHandle } from "@/components/design/Tooltip";

export interface SpanTipEntry {
	title: string;
	/** Service colour for the dot before the meta line. */
	color?: string;
	meta: string;
	/** Error status text, shown in the error colour. */
	error?: string | undefined;
}

export interface SpanTipPayload {
	entries: SpanTipEntry[];
}

const handle = createTooltipHandle<SpanTipPayload>();

export function SpanTip() {
	return (
		<Tooltip handle={handle}>
			{({ payload }) =>
				payload === undefined ? null : (
					<TooltipContent role="tooltip" className="pointer-events-none max-w-80 flex-col items-stretch gap-2">
						{payload.entries.map((entry, i) => (
							<div key={i} className="flex min-w-0 flex-col gap-0.5">
								<span className="line-clamp-2 text-[12.5px] leading-snug font-medium [overflow-wrap:anywhere]">
									{entry.title}
								</span>
								<span className="flex min-w-0 items-center gap-1.5 text-[11px] text-fg3 tabular-nums">
									{entry.color !== undefined && (
										<i
											data-slot="span-tip-dot"
											className="size-[7px] shrink-0 rounded-full"
											style={{ background: entry.color }}
										/>
									)}
									<span className="min-w-0 [overflow-wrap:anywhere]">{entry.meta}</span>
								</span>
								{entry.error !== undefined && (
									<span data-slot="span-tip-error" className="text-[11px] text-bad-fg [overflow-wrap:anywhere]">
										{entry.error}
									</span>
								)}
							</div>
						))}
					</TooltipContent>
				)
			}
		</Tooltip>
	);
}

export function SpanTipTrigger({
	entries,
	render,
	children,
}: {
	entries: SpanTipEntry[];
	render: ReactElement;
	children?: ReactNode;
}) {
	return (
		<TooltipTrigger handle={handle} payload={{ entries }} delay={0} closeDelay={0} render={render}>
			{children}
		</TooltipTrigger>
	);
}
