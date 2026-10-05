import { Input as InputBase } from "@/components/ui/input"
import { cn } from "@/lib/utils"

function Input({ className, ...props }: React.ComponentProps<typeof InputBase>) {
	return (
		<InputBase
			className={cn(
				"h-8 rounded-sm border-line bg-background px-2.5 py-0 text-base placeholder:text-fg4 focus-visible:border-fg4 focus-visible:ring-0 disabled:opacity-55 aria-invalid:border-bad-fg aria-invalid:ring-0 md:text-base dark:bg-background",
				className
			)}
			{...props}
		/>
	)
}

export { Input }
