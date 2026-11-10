import {
	ComboboxContent as ContentBase,
	ComboboxItem as ItemBase,
	ComboboxInput as InputBase,
	ComboboxList as ListBase,
} from "@/components/ui/combobox";
import { cn } from "@/lib/utils";
export {
	Combobox,
	ComboboxChipsInput,
	ComboboxGroup,
	ComboboxLabel,
	ComboboxEmpty,
	useComboboxAnchor,
} from "@/components/ui/combobox";

export function ComboboxContent({ className, ...props }: React.ComponentProps<typeof ContentBase>) {
	return (
		<ContentBase
			className={cn(
				"min-w-(--anchor-width) rounded-card border border-line bg-popover p-1 text-foreground shadow-popover ring-0 dark:border-line2",
				className,
			)}
			{...props}
		/>
	);
}
export function ComboboxList({ className, ...props }: React.ComponentProps<typeof ListBase>) {
	return (
		<ListBase className={cn("max-h-[min(320px,var(--available-height))] overflow-y-auto p-0", className)} {...props} />
	);
}
export function ComboboxItem({ className, ...props }: React.ComponentProps<typeof ItemBase>) {
	return (
		<ItemBase
			className={cn(
				"min-h-8 gap-2.5 rounded-sm px-2 text-[12.5px] text-foreground data-highlighted:bg-panel data-highlighted:text-foreground dark:data-highlighted:bg-panel2 data-disabled:text-fg4",
				className,
			)}
			{...props}
		/>
	);
}
export function ComboboxInput({ className, ...props }: React.ComponentProps<typeof InputBase>) {
	return (
		<InputBase
			className={cn(
				"rounded-control border-line2 bg-raise text-[13px] text-foreground shadow-none has-[[data-slot=input-group-control]:focus-visible]:border-fg3 has-[[data-slot=input-group-control]:focus-visible]:ring-foreground/8",
				className,
			)}
			{...props}
		/>
	);
}
