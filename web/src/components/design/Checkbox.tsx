import { Checkbox as CheckboxBase } from "@/components/ui/checkbox"
import { cn } from "@/lib/utils"

function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxBase>) {
	return (
		<CheckboxBase
			className={cn(
				"size-3.5 rounded-xs border-line2 bg-background focus-visible:ring-2 dark:bg-background [&_[data-slot=checkbox-indicator]>svg]:size-2.5 [&_[data-slot=checkbox-indicator]>svg]:stroke-3",
				className
			)}
			{...props}
		/>
	)
}

export { Checkbox }
