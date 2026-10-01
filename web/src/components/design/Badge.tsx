import { cva, type VariantProps } from "class-variance-authority"

import { Badge as BadgeBase } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

const badgeVariants = cva("rounded-md border", {
	variants: {
		variant: {
			default: "bg-secondary text-secondary-foreground border-border",
			secondary: "bg-secondary text-secondary-foreground border-border",
			primary: "bg-primary text-primary-foreground border-transparent",
			success:
				"bg-status-success text-status-success-foreground border-status-success-border",
			warning:
				"bg-status-warning text-status-warning-foreground border-status-warning-border",
			error:
				"bg-status-error text-status-error-foreground border-status-error-border",
			info: "bg-status-info text-status-info-foreground border-status-info-border",
			running:
				"bg-status-success text-status-success-foreground border-status-success-border",
			pending:
				"bg-status-warning text-status-warning-foreground border-status-warning-border",
			stopped: "border-border bg-muted text-muted-foreground",
			destructive:
				"bg-status-error text-status-error-foreground border-status-error-border",
			outline: "border-border text-foreground bg-transparent",
		},
	},
	defaultVariants: {
		variant: "default",
	},
})

function Badge({
	className,
	variant = "default",
	...props
}: Omit<React.ComponentProps<typeof BadgeBase>, "variant"> &
	VariantProps<typeof badgeVariants>) {
	return (
		<BadgeBase
			variant="outline"
			className={cn(badgeVariants({ variant }), className)}
			{...props}
		/>
	)
}

export { Badge, badgeVariants }
