import {
	Card as CardBase,
	CardAction,
	CardContent,
	CardDescription,
	CardFooter,
	CardHeader,
	CardTitle as CardTitleBase,
} from "@/components/ui/card"
import { cn } from "@/lib/utils"

function Card({ className, ...props }: React.ComponentProps<typeof CardBase>) {
	return <CardBase className={cn("rounded-lg border border-line bg-background text-base shadow-none ring-0", className)} {...props} />
}

function CardTitle({ className, ...props }: React.ComponentProps<typeof CardTitleBase>) {
	return <CardTitleBase className={cn("text-lg font-semibold", className)} {...props} />
}

export { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle }
