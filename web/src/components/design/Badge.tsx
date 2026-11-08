import { cva, type VariantProps } from "class-variance-authority";

import { Badge as BadgeBase } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const badgeVariants = cva("gap-1 rounded-sm border-transparent font-medium [&>svg]:size-3!", {
	variants: {
		size: {
			default: "h-[22px] px-2 text-sm",
			sm: "h-[18px] px-1.5 py-0 text-xs",
		},
		tone: {
			ok: "bg-ok-bg text-ok-fg",
			info: "bg-info-bg text-info-fg",
			warn: "bg-warn-bg text-warn-fg",
			bad: "bg-bad-bg text-bad-fg",
			neutral: "bg-neutral-bg text-neutral-fg",
			muted: "bg-bg3 text-fg3",
			outline: "border-line bg-transparent text-fg2",
			pill: "h-4 border-line2 bg-transparent px-[5px] font-mono text-[10.5px] leading-4 font-normal text-fg3",
			count:
				"h-auto self-center rounded-[5px] bg-panel2 px-1.5 py-px font-mono text-[11.5px] leading-[1.4] font-normal tracking-normal text-fg2 tabular-nums",
		},
	},
	defaultVariants: {
		tone: "neutral",
		size: "default",
	},
});

type BadgeTone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;

function Badge({
	className,
	tone = "neutral",
	size = "default",
	...props
}: Omit<React.ComponentProps<typeof BadgeBase>, "variant"> & {
	tone?: BadgeTone | undefined;
	size?: "default" | "sm" | undefined;
}) {
	return <BadgeBase variant="outline" className={cn(badgeVariants({ tone, size }), className)} {...props} />;
}

export { Badge, badgeVariants, type BadgeTone };
