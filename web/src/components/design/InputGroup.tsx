import {
	InputGroup as InputGroupBase,
	InputGroupAddon as InputGroupAddonBase,
	InputGroupInput as InputGroupInputBase,
	InputGroupText as InputGroupTextBase,
} from "@/components/ui/input-group";
import { cn } from "@/lib/utils";

function InputGroup({ className, ...props }: React.ComponentProps<typeof InputGroupBase>) {
	return (
		<InputGroupBase
			className={cn(
				"h-[34px] rounded-lg border-line2 bg-raise has-disabled:opacity-45 has-[[data-slot=input-group-control]:focus-visible]:border-fg3 has-[[data-slot=input-group-control]:focus-visible]:ring-foreground/8 has-[[data-slot][aria-invalid=true]]:border-err has-[[data-slot][aria-invalid=true]]:ring-0 has-[>[data-align=inline-start]]:[&>input]:pl-2 dark:bg-raise dark:has-[[data-slot][aria-invalid=true]]:ring-0",
				className,
			)}
			{...props}
		/>
	);
}

function InputGroupAddon({ className, ...props }: React.ComponentProps<typeof InputGroupAddonBase>) {
	return (
		<InputGroupAddonBase
			className={cn(
				"font-normal text-fg3 data-[align=inline-start]:pl-2.5 [&>svg:not([class*='size-'])]:size-3.5",
				className,
			)}
			{...props}
		/>
	);
}

function InputGroupInput({ className, ...props }: React.ComponentProps<typeof InputGroupInputBase>) {
	return (
		<InputGroupInputBase
			className={cn("h-full text-[13.5px] text-foreground placeholder:text-fg4 md:text-[13.5px]", className)}
			{...props}
		/>
	);
}

function InputGroupText({ className, ...props }: React.ComponentProps<typeof InputGroupTextBase>) {
	return <InputGroupTextBase className={cn("font-mono text-xs text-fg3", className)} {...props} />;
}

export { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText };
