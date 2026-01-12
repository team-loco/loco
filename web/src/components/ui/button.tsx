import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap text-sm font-medium transition-all focus-visible:outline-none disabled:pointer-events-none disabled:cursor-not-allowed [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 cursor-pointer",
	{
		variants: {
			variant: {
				default:
					"border-2 border-black bg-primary text-primary-foreground shadow-[2px_2px_0px_0px_#000] hover:opacity-90 active:translate-x-1 active:translate-y-1 active:shadow-none dark:border-neutral-700",
				destructive:
					"border-2 border-black bg-destructive text-destructive-foreground shadow-[2px_2px_0px_0px_#000] hover:opacity-90 active:translate-x-1 active:translate-y-1 active:shadow-none dark:border-neutral-700",
				outline:
					"border-2 border-black bg-background text-foreground shadow-[2px_2px_0px_0px_#000] hover:opacity-90 active:translate-x-1 active:translate-y-1 active:shadow-none dark:border-neutral-700",
				secondary:
					"border-2 border-black bg-secondary text-secondary-foreground shadow-[2px_2px_0px_0px_#000] hover:opacity-90 active:translate-x-1 active:translate-y-1 active:shadow-none dark:border-neutral-700",
				ghost: "hover:bg-accent hover:text-accent-foreground",
				link: "text-primary underline-offset-4 hover:underline",
			},
			size: {
				default: "h-9 px-4 py-2 rounded-sm",
				sm: "h-8 px-3 text-xs rounded-sm",
				lg: "h-10 px-8 rounded-sm",
				icon: "h-9 w-9 rounded-sm",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	}
);

export interface ButtonProps
	extends React.ButtonHTMLAttributes<HTMLButtonElement>,
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
	}
);
Button.displayName = "Button";

// eslint-disable-next-line react-refresh/only-export-components
export { Button, buttonVariants };
