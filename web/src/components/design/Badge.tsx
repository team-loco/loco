import { cva, type VariantProps } from "class-variance-authority"

import { Badge as BadgeBase } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

const badgeVariants = cva("gap-1 rounded-sm border-transparent font-medium [&>svg]:size-3!", {
	variants: {
		tone: {
			ok: "bg-ok-bg text-ok-fg",
			info: "bg-info-bg text-info-fg",
			warn: "bg-warn-bg text-warn-fg",
			bad: "bg-bad-bg text-bad-fg",
			neutral: "bg-neutral-bg text-neutral-fg",
			muted: "bg-bg3 text-fg3",
			outline: "border-line bg-transparent text-fg2",
		},
		size: {
			default: "h-[22px] px-2 text-sm",
			sm: "h-[18px] px-1.5 py-0 text-xs",
		},
	},
	defaultVariants: {
		tone: "neutral",
		size: "default",
	},
})

type BadgeTone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>

function Badge({
	className,
	tone = "neutral",
	size = "default",
	...props
}: Omit<React.ComponentProps<typeof BadgeBase>, "variant"> & {
	tone?: BadgeTone | undefined
	size?: "default" | "sm" | undefined
}) {
	return <BadgeBase variant="outline" className={cn(badgeVariants({ tone, size }), className)} {...props} />
}

export { Badge, badgeVariants, type BadgeTone }
