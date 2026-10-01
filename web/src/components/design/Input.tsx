import { Input as InputBase } from "@/components/ui/input"
import { cn } from "@/lib/utils"

function Input({ className, ...props }: React.ComponentProps<typeof InputBase>) {
	return (
		<InputBase className={cn("bg-background text-sm md:text-sm", className)} {...props} />
	)
}

export { Input }
