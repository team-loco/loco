import { cva, type VariantProps } from "class-variance-authority";

import { Button as ButtonBase } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type BaseVariant = "default" | "outline" | "ghost" | "destructive" | "link";
type BaseSize = "default" | "xs" | "sm" | "lg" | "icon" | "icon-xs" | "icon-sm" | "icon-lg";

const solid = "border-0 font-medium hover:opacity-86 dark:hover:opacity-100 dark:hover:brightness-108";

const buttonVariants = cva(
	"gap-1.5 rounded-control active:not-aria-[haspopup]:translate-y-0 focus-visible:ring-2 disabled:pointer-events-auto disabled:cursor-not-allowed disabled:opacity-45 [&_svg:not([class*='size-'])]:size-3.5",
	{
		variants: {
			variant: {
				default: cn(solid, "bg-primary text-primary-foreground hover:bg-primary"),
				outline:
					"border-line2 bg-raise font-normal text-foreground hover:border-fg4 hover:bg-raise aria-expanded:border-fg4 aria-expanded:bg-raise dark:border-line2 dark:bg-raise dark:hover:border-line2 dark:hover:bg-panel2 dark:aria-expanded:bg-panel2",
				ghost:
					"bg-transparent font-normal text-fg2 hover:bg-panel2 hover:text-foreground aria-expanded:bg-panel2 aria-expanded:text-foreground dark:hover:bg-panel2",
				destructive: cn(solid, "bg-err text-background hover:bg-err dark:bg-err dark:hover:bg-err"),
				"destructive-outline":
					"border-[color-mix(in_oklch,var(--err)_38%,var(--line2))] bg-raise font-normal text-bad-fg hover:border-[color-mix(in_oklch,var(--err)_70%,var(--line2))] hover:bg-[color-mix(in_oklch,var(--err)_6%,var(--raise))] hover:text-bad-fg dark:border-[color-mix(in_oklch,var(--err)_38%,var(--line2))] dark:bg-raise dark:hover:border-[color-mix(in_oklch,var(--err)_70%,var(--line2))] dark:hover:bg-[color-mix(in_oklch,var(--err)_6%,var(--raise))]",
				link: "font-normal text-fg2 underline decoration-line2 underline-offset-3 hover:text-foreground hover:decoration-foreground",
			},
			size: {
				default: "h-8 px-[13px] text-[13px]",
				xs: "h-6.5 px-2 text-xs",
				sm: "h-[30px] px-2.5 text-[12.5px]",
				lg: "h-[34px] px-4 text-[13px]",
				xl: "h-[42px] px-4 text-md font-semibold",
				icon: "size-[30px]",
				"icon-sm": "size-7",
				"icon-xs": "size-6",
			},
		},
		compoundVariants: [
			{ variant: "outline", size: ["icon", "icon-sm", "icon-xs"], class: "text-fg2 hover:text-foreground" },
			{ variant: "ghost", size: ["icon", "icon-sm", "icon-xs"], class: "text-fg3" },
			{ variant: "link", class: "h-auto p-0 text-[12.5px]" },
		],
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	},
);

type ButtonVariant = NonNullable<VariantProps<typeof buttonVariants>["variant"]>;
type ButtonSize = NonNullable<VariantProps<typeof buttonVariants>["size"]>;

function baseVariant(variant: ButtonVariant): BaseVariant {
	switch (variant) {
		case "default":
			return "default";
		case "outline":
			return "outline";
		case "ghost":
			return "ghost";
		case "destructive":
			return "destructive";
		case "destructive-outline":
			return "outline";
		case "link":
			return "link";
	}
}

function baseSize(size: ButtonSize): BaseSize {
	switch (size) {
		case "default":
			return "default";
		case "xs":
			return "xs";
		case "sm":
			return "sm";
		case "lg":
			return "lg";
		case "xl":
			return "lg";
		case "icon":
			return "icon";
		case "icon-sm":
			return "icon-sm";
		case "icon-xs":
			return "icon-xs";
	}
}

function Button({
	className,
	variant = "default",
	size = "default",
	...props
}: Omit<React.ComponentProps<typeof ButtonBase>, "variant" | "size"> & {
	variant?: ButtonVariant | undefined;
	size?: ButtonSize | undefined;
}) {
	return (
		<ButtonBase
			variant={baseVariant(variant)}
			size={baseSize(size)}
			className={cn(buttonVariants({ variant, size }), className)}
			{...props}
		/>
	);
}

export { Button, type ButtonVariant };
