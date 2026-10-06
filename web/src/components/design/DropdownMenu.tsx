import {
	DropdownMenu,
	DropdownMenuCheckboxItem as DropdownMenuCheckboxItemBase,
	DropdownMenuContent as DropdownMenuContentBase,
	DropdownMenuGroup,
	DropdownMenuItem as DropdownMenuItemBase,
	DropdownMenuLabel as DropdownMenuLabelBase,
	DropdownMenuPortal,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem as DropdownMenuRadioItemBase,
	DropdownMenuSeparator as DropdownMenuSeparatorBase,
	DropdownMenuShortcut,
	DropdownMenuSub,
	DropdownMenuSubContent as DropdownMenuSubContentBase,
	DropdownMenuSubTrigger as DropdownMenuSubTriggerBase,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"

const popup = "rounded-lg border border-line bg-background p-1 text-foreground shadow-popover ring-0"
const item =
	"h-8 cursor-pointer gap-2 rounded-sm px-2.5 py-0 text-base data-disabled:pointer-events-auto data-disabled:cursor-not-allowed focus:bg-bg3 focus:text-foreground not-data-[variant=destructive]:focus:**:text-foreground data-popup-open:bg-bg3 data-open:bg-bg3 [&_svg:not([class*='size-'])]:size-3.5"

function DropdownMenuContent({ className, ...props }: React.ComponentProps<typeof DropdownMenuContentBase>) {
	return <DropdownMenuContentBase className={cn(popup, "min-w-[180px]", className)} {...props} />
}

function DropdownMenuSubContent({ className, ...props }: React.ComponentProps<typeof DropdownMenuSubContentBase>) {
	return <DropdownMenuSubContentBase className={cn(popup, className)} {...props} />
}

function DropdownMenuItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuItemBase>) {
	return (
		<DropdownMenuItemBase
			className={cn(
				item,
				"data-[variant=destructive]:text-red data-[variant=destructive]:focus:bg-bg3 data-[variant=destructive]:focus:text-red dark:data-[variant=destructive]:focus:bg-bg3 data-[variant=destructive]:*:[svg]:text-red",
				className
			)}
			{...props}
		/>
	)
}

function DropdownMenuSubTrigger({ className, ...props }: React.ComponentProps<typeof DropdownMenuSubTriggerBase>) {
	return <DropdownMenuSubTriggerBase className={cn(item, className)} {...props} />
}

function DropdownMenuCheckboxItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuCheckboxItemBase>) {
	return <DropdownMenuCheckboxItemBase className={cn(item, "pr-8", className)} {...props} />
}

function DropdownMenuRadioItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuRadioItemBase>) {
	return <DropdownMenuRadioItemBase className={cn(item, "pr-8", className)} {...props} />
}

function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof DropdownMenuLabelBase>) {
	return (
		<DropdownMenuLabelBase
			className={cn("px-2.5 pt-2 pb-1 text-xs font-normal tracking-[0.04em] text-fg3 uppercase", className)}
			{...props}
		/>
	)
}

function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof DropdownMenuSeparatorBase>) {
	return <DropdownMenuSeparatorBase className={cn("my-1 bg-line", className)} {...props} />
}

export {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuPortal,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuShortcut,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
}
