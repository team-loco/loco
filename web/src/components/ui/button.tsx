import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap font-medium transition-shadow duration-200 focus-visible:outline-none disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 cursor-pointer relative",
	{
		variants: {
			variant: {
				default:
					"bg-[#0f0f0f] text-[#f5f5f5] rounded-[10px] font-medium tracking-[0.01em]",
				destructive:
					"bg-destructive text-white shadow-xs hover:shadow-sm active:shadow-xs",
				outline:
					"border border-border bg-card text-foreground shadow-xs hover:bg-accent hover:border-border-strong hover:shadow-sm rounded-[10px]",
				secondary:
					"bg-secondary text-secondary-foreground border border-border shadow-xs hover:bg-accent hover:border-border-strong",
				ghost: "hover:bg-accent hover:text-accent-foreground",
				link: "text-primary underline-offset-4 hover:underline",
			},
			size: {
				default: "px-4 py-2 text-sm",
				sm: "px-4 py-2 text-xs rounded-md",
				lg: "px-6 py-3 text-base rounded-md",
				icon: "h-7 w-9 rounded-md",
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

const Button = ({
	ref,
	className,
	variant,
	size,
	asChild = false,
	...props
}: ButtonProps & { ref?: React.RefObject<HTMLButtonElement | null> }) => {
	const Comp = asChild ? Slot : "button";
	return (
		<Comp
			className={cn(buttonVariants({ variant, size, className }))}
			ref={ref}
			{...props}
		/>
	);
};
Button.displayName = "Button";
// eslint-disable-next-line react-refresh/only-export-components
export { Button, buttonVariants };
