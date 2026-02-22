import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-lg px-[13px] py-[7px] text-xs font-bold w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1.5 [&>svg]:pointer-events-none transition-all duration-fast overflow-hidden focus-visible:outline-none uppercase tracking-wider",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground shadow-xs",
				secondary:
					"bg-secondary text-secondary-foreground border border-border",
				destructive: "bg-destructive text-white shadow-xs",
				outline: "border border-border text-foreground",
				success:
					"bg-success-light text-success border border-success-border shadow-xs",
				warning:
					"bg-warning-light text-warning border border-warning-border shadow-xs",
				error:
					"bg-error-light text-error border border-error-border shadow-xs",
				info: "bg-info-light text-info border border-info-border shadow-xs",
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
