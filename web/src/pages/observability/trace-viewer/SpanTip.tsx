import type { ReactElement, ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger, createTooltipHandle } from "@/components/design/Tooltip";

export interface SpanTipLines {
	lines: string[];
}

const handle = createTooltipHandle<SpanTipLines>();

export function SpanTip() {
	return (
		<Tooltip handle={handle}>
			{({ payload }) =>
				payload === undefined ? null : (
					<TooltipContent
						role="tooltip"
						className="pointer-events-none max-w-sm flex-col items-start gap-0.5 font-mono text-[11.5px]"
					>
						{payload.lines.map((line, i) => (
							<span key={i} className="max-w-full [overflow-wrap:anywhere]">
								{line}
							</span>
						))}
					</TooltipContent>
				)
			}
		</Tooltip>
	);
}

export function SpanTipTrigger({
	lines,
	render,
	children,
}: {
	lines: string[];
	render: ReactElement;
	children?: ReactNode;
}) {
	return (
		<TooltipTrigger handle={handle} payload={{ lines }} delay={0} closeDelay={0} render={render}>
			{children}
		</TooltipTrigger>
	);
}
