import { cva, type VariantProps } from "class-variance-authority";

import { Toggle } from "@/components/ui/toggle";
import { cn } from "@/lib/utils";

const chip = cva("border border-line2 font-normal aria-pressed:text-foreground", {
	variants: {
		variant: {
			button:
				"h-8 gap-1.5 rounded-control bg-raise px-[13px] text-[13px] text-foreground hover:border-fg4 hover:bg-panel2 focus-visible:ring-2 disabled:opacity-45 aria-pressed:bg-panel2 [&_svg:not([class*='size-'])]:size-3.5",
			pill: "h-[30px] gap-1.5 rounded-full px-3 text-[13px] text-fg2 hover:border-fg3 hover:bg-transparent aria-pressed:border-fg2 aria-pressed:bg-panel2",
			event:
				"h-[26px] rounded-md border-dashed px-2.5 font-mono text-[12px] text-fg3 line-through decoration-fg3/50 aria-pressed:border-solid aria-pressed:bg-background aria-pressed:no-underline",
		},
	},
	defaultVariants: { variant: "pill" },
});

function ToggleChip({
	className,
	variant,
	...props
}: Omit<React.ComponentProps<typeof Toggle>, "variant" | "size"> & VariantProps<typeof chip>) {
	return <Toggle className={cn(chip({ variant }), className)} {...props} />;
}

export { ToggleChip };
