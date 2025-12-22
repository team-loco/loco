import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-sm px-2.5 text-xs font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1 [&>svg]:pointer-events-none transition-all duration-75 overflow-hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground hover:opacity-90",
				secondary:
					"bg-secondary text-secondary-foreground border border-border hover:bg-accent",
				destructive: "bg-destructive text-white hover:opacity-90",
				outline: "border border-border text-foreground hover:bg-accent",
				success:
					"bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200",
				warning:
					"bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
			},
		},
		defaultVariants: {
			variant: "default",
		},
	}
);

function Badge({
	className,
	variant,
	asChild = false,
	...props
}: React.ComponentProps<"span"> &
	VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
	const Comp = asChild ? Slot : "span";

	return (
		<Comp
			data-slot="badge"
			className={cn(badgeVariants({ variant }), className)}
			{...props}
		/>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export { Badge, badgeVariants };
