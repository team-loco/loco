import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
	// Base classes
	"inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
	{
		variants: {
			variant: {
				default:
					"bg-primary text-primary-foreground hover:opacity-90 active:opacity-75",
				destructive:
					"bg-destructive text-white hover:opacity-90 active:opacity-75",
				outline:
					"border border-border bg-background hover:bg-accent text-foreground",
				secondary:
					"bg-secondary text-secondary-foreground border border-border hover:bg-accent",
				ghost: "hover:bg-accent text-foreground bg-transparent",
				link: "text-primary underline-offset-4 hover:underline bg-transparent px-0 py-0 h-auto",
			},
			size: {
				default: "h-7 px-4 py-2 gap-2 has-[>svg]:px-2.5",
				sm: "h-6 px-3.5 py-1 gap-1.5 has-[>svg]:px-2",
				lg: "h-8.5 px-4 py-2 gap-2.5 has-[>svg]:px-3",
				icon: "h-6.5 w-8 p-2",
				"icon-sm": "h-7 w-7 p-1.5",
				"icon-lg": "h-9 w-9 p-2.5",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	}
);

type ButtonProps = React.ComponentProps<"button"> &
	VariantProps<typeof buttonVariants> & {
		asChild?: boolean;
	};

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
	({ className, variant, size, asChild = false, ...props }, ref) => {
		const Comp = asChild ? Slot : "button";
		return (
			<Comp
				ref={ref}
				data-slot="button"
				className={cn(buttonVariants({ variant, size, className }))}
				{...props}
			/>
		);
	}
);

Button.displayName = "Button";

export { Button, buttonVariants };
