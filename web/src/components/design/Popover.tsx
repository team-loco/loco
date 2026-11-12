import {
	Popover,
	PopoverContent as PopoverContentBase,
	PopoverTrigger as PopoverTriggerBase,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

function PopoverTrigger({ className, ...props }: React.ComponentProps<typeof PopoverTriggerBase>) {
	return (
		<PopoverTriggerBase
			className={cn(
				"cursor-pointer disabled:pointer-events-auto disabled:cursor-not-allowed data-disabled:cursor-not-allowed",
				className,
			)}
			{...props}
		/>
	);
}

function PopoverContent({ className, ...props }: React.ComponentProps<typeof PopoverContentBase>) {
	return (
		<PopoverContentBase
			className={cn(
				"rounded-overlay border border-line bg-popover text-[13px] text-foreground shadow-popover ring-0 dark:border-line2",
				className,
			)}
			{...props}
		/>
	);
}

export { Popover, PopoverContent, PopoverTrigger };
