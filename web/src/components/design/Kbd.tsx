import { Kbd as KbdBase, KbdGroup } from "@/components/ui/kbd"
import { cn } from "@/lib/utils"

function Kbd({ className, ...props }: React.ComponentProps<typeof KbdBase>) {
	return (
		<KbdBase
			className={cn("h-auto min-w-0 rounded-xs border border-line bg-transparent px-1.5 font-normal text-fg3", className)}
			{...props}
		/>
	)
}

export { Kbd, KbdGroup }
