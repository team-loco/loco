import {
	Tooltip,
	TooltipContent as TooltipContentBase,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

function TooltipContent({ className, ...props }: React.ComponentProps<typeof TooltipContentBase>) {
	return <TooltipContentBase className={cn("rounded-sm px-2 py-1 text-sm", className)} {...props} />
}

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger }
