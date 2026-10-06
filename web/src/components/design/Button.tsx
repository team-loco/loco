import { cva, type VariantProps } from "class-variance-authority"

import { Button as ButtonBase } from "@/components/ui/button"
import { cn } from "@/lib/utils"

type BaseVariant = "default" | "outline" | "secondary" | "ghost" | "destructive" | "link"
type BaseSize = "default" | "xs" | "sm" | "lg" | "icon" | "icon-xs" | "icon-sm" | "icon-lg"

const buttonVariants = cva(
	"cursor-pointer gap-2 rounded-sm text-base active:not-aria-[haspopup]:translate-y-0 focus-visible:ring-2 disabled:pointer-events-auto disabled:cursor-not-allowed disabled:opacity-55 [&_svg:not([class*='size-'])]:size-3.5",
	{
		variants: {
			variant: {
				default:
					"border-primary bg-primary text-primary-foreground hover:border-primary-hover hover:bg-primary-hover",
				outline:
					"border-line bg-background font-normal hover:border-fg4 hover:bg-background aria-expanded:border-foreground aria-expanded:bg-background dark:border-line dark:bg-background dark:hover:bg-background",
				secondary: "border-line bg-background font-normal text-fg2 hover:bg-bg3",
				ghost: "font-normal hover:bg-bg3 aria-expanded:bg-bg3 dark:hover:bg-bg3",
				destructive: "border-red bg-red text-white hover:bg-red hover:opacity-90 dark:bg-red dark:hover:bg-red",
				"destructive-outline":
					"border-[color-mix(in_oklab,var(--red)_45%,transparent)] bg-background text-red hover:bg-bad-bg dark:bg-background",
				inverted: "border-foreground bg-foreground text-background hover:bg-foreground hover:opacity-90",
				link: "h-auto p-0 font-normal text-fg3 underline hover:text-foreground",
			},
			size: {
				default: "h-8 px-3",
				xs: "h-6.5 px-2.5 text-sm",
				sm: "h-7 px-2.5 text-sm",
				lg: "h-[34px] rounded-lg px-4",
				xl: "h-[42px] rounded-lg px-4 text-md font-semibold",
				icon: "size-8",
				"icon-sm": "size-7",
				"icon-xs": "size-6",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	}
)

type ButtonVariant = NonNullable<VariantProps<typeof buttonVariants>["variant"]>
type ButtonSize = NonNullable<VariantProps<typeof buttonVariants>["size"]>

function baseVariant(variant: ButtonVariant): BaseVariant {
	switch (variant) {
		case "default":
			return "default"
		case "outline":
			return "outline"
		case "secondary":
			return "secondary"
		case "ghost":
			return "ghost"
		case "destructive":
			return "destructive"
		case "destructive-outline":
			return "outline"
		case "inverted":
			return "default"
		case "link":
			return "link"
	}
}

function baseSize(size: ButtonSize): BaseSize {
	switch (size) {
		case "default":
			return "default"
		case "xs":
			return "xs"
		case "sm":
			return "sm"
		case "lg":
			return "lg"
		case "xl":
			return "lg"
		case "icon":
			return "icon"
		case "icon-sm":
			return "icon-sm"
		case "icon-xs":
			return "icon-xs"
	}
}

function Button({
	className,
	variant = "default",
	size = "default",
	...props
}: Omit<React.ComponentProps<typeof ButtonBase>, "variant" | "size"> & {
	variant?: ButtonVariant | undefined
	size?: ButtonSize | undefined
}) {
	return (
		<ButtonBase
			variant={baseVariant(variant)}
			size={baseSize(size)}
			className={cn(buttonVariants({ variant, size }), className)}
			{...props}
		/>
	)
}

export { Button, buttonVariants, type ButtonVariant, type ButtonSize }
