import {
	Select,
	SelectContent as SelectContentBase,
	SelectGroup,
	SelectItem as SelectItemBase,
	SelectLabel as SelectLabelBase,
	SelectSeparator as SelectSeparatorBase,
	SelectTrigger as SelectTriggerBase,
	SelectValue,
} from "@/components/ui/select"
import { cn } from "@/lib/utils"

function SelectTrigger({ className, ...props }: React.ComponentProps<typeof SelectTriggerBase>) {
	return (
		<SelectTriggerBase
			className={cn(
				"rounded-sm border-line bg-background pr-2 pl-2.5 text-base hover:border-fg4 focus-visible:border-fg4 focus-visible:ring-0 data-placeholder:text-fg4 data-[size=default]:h-8 dark:bg-background dark:hover:bg-background [&_svg:not([class*='size-'])]:size-3.5",
				className
			)}
			{...props}
		/>
	)
}

function SelectContent({ className, ...props }: React.ComponentProps<typeof SelectContentBase>) {
	return (
		<SelectContentBase
			className={cn("rounded-lg border border-line bg-background p-1 shadow-popover ring-0", className)}
			{...props}
		/>
	)
}

function SelectItem({ className, ...props }: React.ComponentProps<typeof SelectItemBase>) {
	return (
		<SelectItemBase
			className={cn("h-8 rounded-sm px-2.5 text-base focus:bg-bg3 focus:text-foreground", className)}
			{...props}
		/>
	)
}

function SelectLabel({ className, ...props }: React.ComponentProps<typeof SelectLabelBase>) {
	return (
		<SelectLabelBase
			className={cn("px-2.5 pt-2 pb-1 text-xs tracking-[0.04em] text-fg3 uppercase", className)}
			{...props}
		/>
	)
}

function SelectSeparator({ className, ...props }: React.ComponentProps<typeof SelectSeparatorBase>) {
	return <SelectSeparatorBase className={cn("bg-line", className)} {...props} />
}

export { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectSeparator, SelectTrigger, SelectValue }
