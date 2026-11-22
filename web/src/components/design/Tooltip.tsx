import {
	Tooltip,
	TooltipContent as TooltipContentBase,
	TooltipProvider,
	TooltipTrigger,
	createTooltipHandle,
} from "@/components/ui/tooltip";
import type { ReactElement } from "react";

import { cn } from "@/lib/utils";

function TooltipContent({ className, ...props }: React.ComponentProps<typeof TooltipContentBase>) {
	return (
		<TooltipContentBase
			className={cn(
				"rounded-lg border border-line bg-background px-[9px] py-[5px] text-[12.5px] text-foreground shadow-popover [&>[data-side]]:hidden",
				className,
			)}
			{...props}
		/>
	);
}

/** Shows tip in a themed tooltip; a wrapping span takes the hover, so disabled controls show it too. */
function Tip({
	tip,
	className,
	children,
}: {
	tip: string | null | undefined;
	className?: string;
	children: ReactElement;
}) {
	if (tip === null || tip === undefined) return children;
	return (
		<Tooltip>
			<TooltipTrigger render={<span className={cn("inline-flex", className)} />}>{children}</TooltipTrigger>
			<TooltipContent className="max-w-[280px]">{tip}</TooltipContent>
		</Tooltip>
	);
}

export { Tip, Tooltip, TooltipContent, TooltipProvider, TooltipTrigger, createTooltipHandle };
