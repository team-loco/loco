import { Textarea as TextareaBase } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

function Textarea({ className, ...props }: React.ComponentProps<typeof TextareaBase>) {
	return (
		<TextareaBase
			className={cn(
				"field-sizing-fixed min-h-0 resize-y rounded-sm border-line bg-background px-2.5 py-2 text-base placeholder:text-fg4 focus-visible:border-fg4 focus-visible:ring-0 aria-invalid:border-bad-fg aria-invalid:ring-0 md:text-base dark:bg-background",
				className
			)}
			{...props}
		/>
	)
}

export { Textarea }
