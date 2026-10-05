import { Switch as SwitchBase } from "@/components/ui/switch"
import { cn } from "@/lib/utils"

function Switch({ className, ...props }: React.ComponentProps<typeof SwitchBase>) {
	return (
		<SwitchBase
			className={cn(
				"focus-visible:ring-2 data-checked:bg-primary data-unchecked:bg-line2 dark:data-unchecked:bg-line2 data-disabled:opacity-55 [&_[data-slot=switch-thumb]]:bg-white [&_[data-slot=switch-thumb]]:shadow-[0_1px_2px_rgba(0,0,0,0.2)] dark:[&_[data-slot=switch-thumb]]:bg-white",
				className
			)}
			{...props}
		/>
	)
}

export { Switch }
