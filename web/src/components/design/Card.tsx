import {
	Card as CardBase,
	CardAction,
	CardContent,
	CardDescription,
	CardFooter,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { cn } from "@/lib/utils"

function Card({ className, ...props }: React.ComponentProps<typeof CardBase>) {
	return (
		<CardBase
			className={cn("border border-border shadow-xs ring-0", className)}
			{...props}
		/>
	)
}

export {
	Card,
	CardHeader,
	CardFooter,
	CardTitle,
	CardAction,
	CardDescription,
	CardContent,
}
