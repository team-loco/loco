import { Popover, PopoverContent as PopoverContentBase, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

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
