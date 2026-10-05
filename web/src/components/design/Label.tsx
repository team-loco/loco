import { Label as LabelBase } from "@/components/ui/label"
import { cn } from "@/lib/utils"

function Label({ className, ...props }: React.ComponentProps<typeof LabelBase>) {
	return (
		<LabelBase
			className={cn("flex-col items-stretch gap-1.5 text-sm leading-normal font-normal text-fg3", className)}
			{...props}
		/>
	)
}

export { Label }
