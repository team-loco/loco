import { RadioGroup as RadioGroupBase, RadioGroupItem as RadioGroupItemBase } from "@/components/ui/radio-group";
import { cn } from "@/lib/utils";

export function RadioGroup({ className, ...props }: React.ComponentProps<typeof RadioGroupBase>) {
	return <RadioGroupBase className={cn("gap-3", className)} {...props} />;
}

export function RadioCard({ className, children, ...props }: React.ComponentProps<typeof RadioGroupItemBase>) {
	return (
		<RadioGroupItemBase
			className={cn(
				"aspect-auto h-auto w-auto shrink flex-col gap-2 rounded-card border-0 bg-raise px-1.5 pt-1.5 pb-2 text-left text-foreground ring-1 ring-line2 hover:ring-fg4 data-checked:bg-raise data-checked:text-foreground dark:bg-raise dark:data-checked:bg-raise data-checked:ring-2 data-checked:ring-foreground focus-visible:ring-2 focus-visible:ring-foreground after:hidden [&>[data-slot=radio-group-indicator]]:hidden",
				className,
			)}
			{...props}
			render={(buttonProps) => (
				<button {...buttonProps} type="button">
					{children}
				</button>
			)}
		/>
	);
}
