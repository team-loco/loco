import { cva, type VariantProps } from "class-variance-authority"

import { Button as ButtonBase, buttonVariants } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const buttonOverrides = cva("cursor-pointer font-semibold disabled:cursor-not-allowed", {
	variants: {
		variant: {
			default: "shadow-sm hover:bg-primary/90 hover:shadow-md active:shadow-none",
			outline: "",
			secondary: "",
			ghost: "hover:text-current aria-expanded:text-current",
			destructive: "",
			link: "",
		},
		size: {
			default: "px-3",
			xs: "",
			sm: "",
			lg: "h-10 gap-2 px-4 text-base has-data-[icon=inline-end]:pr-3 has-data-[icon=inline-start]:pl-3",
			icon: "",
			"icon-xs": "",
			"icon-sm": "",
			"icon-lg": "",
		},
	},
	defaultVariants: {
		variant: "default",
		size: "default",
	},
})

function Button({
	className,
	variant = "default",
	size = "default",
	...props
}: React.ComponentProps<typeof ButtonBase> & VariantProps<typeof buttonOverrides>) {
	return (
		<ButtonBase
			variant={variant}
			size={size}
			className={cn(buttonOverrides({ variant, size }), className)}
			{...props}
		/>
	)
}

export { Button, buttonVariants }
