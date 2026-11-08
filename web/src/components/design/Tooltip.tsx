import {
	Tooltip,
	TooltipContent as TooltipContentBase,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/ui/tooltip";
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

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger };
