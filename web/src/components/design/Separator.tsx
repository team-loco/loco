import { Separator as SeparatorBase } from "@/components/ui/separator"
import { cn } from "@/lib/utils"

function Separator({ className, ...props }: React.ComponentProps<typeof SeparatorBase>) {
	return <SeparatorBase className={cn("bg-line", className)} {...props} />
}

export { Separator }
