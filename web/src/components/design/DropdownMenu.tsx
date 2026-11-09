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
	DropdownMenuSub,
	DropdownMenuSubContent as DropdownMenuSubContentBase,
	DropdownMenuSubTrigger as DropdownMenuSubTriggerBase,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

const popup =
	"rounded-[10px] border border-line bg-popover p-[5px] text-foreground shadow-popover ring-0 dark:border-line2";
const item =
	"min-h-[34px] gap-2.5 rounded-lg px-2.5 py-1.5 text-[13.5px] focus:bg-panel focus:text-foreground not-data-[variant=destructive]:focus:**:text-foreground data-disabled:pointer-events-auto data-popup-open:bg-panel data-open:bg-panel dark:focus:bg-panel2 dark:data-popup-open:bg-panel2 dark:data-open:bg-panel2 [&_svg:not([class*='size-'])]:size-3.5";

function DropdownMenuContent({ className, ...props }: React.ComponentProps<typeof DropdownMenuContentBase>) {
	return <DropdownMenuContentBase className={cn(popup, "min-w-[180px]", className)} {...props} />;
}

function DropdownMenuSubContent({ className, ...props }: React.ComponentProps<typeof DropdownMenuSubContentBase>) {
	return <DropdownMenuSubContentBase className={cn(popup, className)} {...props} />;
}

function DropdownMenuItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuItemBase>) {
	return (
		<DropdownMenuItemBase
			className={cn(
				item,
				"data-[variant=destructive]:text-err data-[variant=destructive]:focus:bg-panel data-[variant=destructive]:focus:text-err dark:data-[variant=destructive]:focus:bg-panel2 data-[variant=destructive]:*:[svg]:text-err",
				className,
			)}
			{...props}
		/>
	);
}

function DropdownMenuSubTrigger({ className, ...props }: React.ComponentProps<typeof DropdownMenuSubTriggerBase>) {
	return <DropdownMenuSubTriggerBase className={cn(item, className)} {...props} />;
}

function DropdownMenuCheckboxItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuCheckboxItemBase>) {
	return <DropdownMenuCheckboxItemBase className={cn(item, "pr-8", className)} {...props} />;
}

function DropdownMenuRadioItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuRadioItemBase>) {
	return <DropdownMenuRadioItemBase className={cn(item, "pr-8", className)} {...props} />;
}

function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof DropdownMenuLabelBase>) {
	return (
		<DropdownMenuLabelBase className={cn("px-2 pt-2.5 pb-1 text-[12px] font-normal text-fg3", className)} {...props} />
	);
}

function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof DropdownMenuSeparatorBase>) {
	return <DropdownMenuSeparatorBase className={cn("my-1 bg-line", className)} {...props} />;
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
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
};
