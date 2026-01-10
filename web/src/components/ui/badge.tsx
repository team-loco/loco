import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-sm px-2.5 text-xs font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1 [&>svg]:pointer-events-none transition-all duration-75 overflow-hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground",
				secondary:
					"bg-secondary text-secondary-foreground border border-border",
				destructive: "bg-destructive text-destructive-foreground",
				outline: "border border-border text-foreground",
				success:
					"bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200",
				warning:
					"bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
				// Neobrutalist variants
				"neo-blue": "bg-blue-100 text-blue-900 border-2 border-blue-900 shadow-[2px_2px_0px_0px_rgba(30,64,175,1)] dark:bg-blue-900 dark:text-blue-100 dark:border-blue-700 dark:shadow-[2px_2px_0px_0px_rgba(30,58,138,1)]",
				"neo-purple": "bg-purple-100 text-purple-900 border-2 border-purple-900 shadow-[2px_2px_0px_0px_rgba(88,28,135,1)] dark:bg-purple-900 dark:text-purple-100 dark:border-purple-700 dark:shadow-[2px_2px_0px_0px_rgba(88,28,135,1)]",
				"neo-green": "bg-green-100 text-green-900 border-2 border-green-900 shadow-[2px_2px_0px_0px_rgba(20,83,45,1)] dark:bg-green-900 dark:text-green-100 dark:border-green-700 dark:shadow-[2px_2px_0px_0px_rgba(21,128,61,1)]",
				"neo-orange": "bg-orange-100 text-orange-900 border-2 border-orange-900 shadow-[2px_2px_0px_0px_rgba(124,45,18,1)] dark:bg-orange-900 dark:text-orange-100 dark:border-orange-700 dark:shadow-[2px_2px_0px_0px_rgba(124,45,18,1)]",
				"neo-red": "bg-red-100 text-red-900 border-2 border-red-900 shadow-[2px_2px_0px_0px_rgba(127,29,29,1)] dark:bg-red-900 dark:text-red-100 dark:border-red-700 dark:shadow-[2px_2px_0px_0px_rgba(127,29,29,1)]",
				"neo-gray": "bg-gray-100 text-gray-900 border-2 border-gray-900 shadow-[2px_2px_0px_0px_rgba(17,24,39,1)] dark:bg-gray-800 dark:text-gray-100 dark:border-gray-600 dark:shadow-[2px_2px_0px_0px_rgba(55,65,81,1)]",
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
