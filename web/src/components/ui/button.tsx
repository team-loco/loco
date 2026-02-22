import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap text-sm font-semibold transition-all duration-fast focus-visible:outline-none disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 cursor-pointer",
	{
		variants: {
			variant: {
				default:
					"bg-gradient-to-br from-primary to-primary-hover text-primary-foreground shadow-sm hover:shadow-md hover:-translate-y-px active:translate-y-0",
				destructive:
					"bg-gradient-to-br from-destructive to-destructive/90 text-white shadow-sm hover:shadow-md hover:-translate-y-px active:translate-y-0",
				outline:
					"border border-border bg-card text-foreground shadow-xs hover:bg-accent hover:border-border-strong hover:shadow-sm",
				secondary:
					"bg-secondary text-secondary-foreground border border-border shadow-xs hover:bg-accent hover:border-border-strong",
				ghost: "hover:bg-accent hover:text-accent-foreground",
				link: "text-primary underline-offset-4 hover:underline",
			},
			size: {
				default: "h-11 px-[22px] py-3 rounded-md",
				sm: "h-9 px-4 text-xs rounded-md",
				lg: "h-12 px-6 rounded-md",
				icon: "h-9 w-9 rounded-md",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	},
);

export interface ButtonProps
	extends
		React.ButtonHTMLAttributes<HTMLButtonElement>,
		VariantProps<typeof buttonVariants> {
	asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
	({ className, variant, size, asChild = false, ...props }, ref) => {
		const Comp = asChild ? Slot : "button";
		return (
			<Comp
				className={cn(buttonVariants({ variant, size, className }))}
				ref={ref}
				{...props}
			/>
		);
	},
);
Button.displayName = "Button";

// eslint-disable-next-line react-refresh/only-export-components
// eslint-disable-next-line react-refresh/only-export-components
export { Button, buttonVariants };
