import {
	Command as CommandBase,
	CommandDialog as CommandDialogBase,
	CommandEmpty as CommandEmptyBase,
	CommandGroup as CommandGroupBase,
	CommandInput as CommandInputBase,
	CommandItem as CommandItemBase,
	CommandList as CommandListBase,
	CommandShortcut as CommandShortcutBase,
} from "@/components/ui/command";
import { modalSurface } from "@/components/design/Dialog";
import { cn } from "@/lib/utils";

function CommandDialog({ className, ...props }: React.ComponentProps<typeof CommandDialogBase>) {
	return (
		<CommandDialogBase
			className={cn(
				modalSurface,
				"top-[14vh] flex w-[620px] flex-col rounded-overlay! border-line2 sm:max-w-[calc(100%-32px)] dark:bg-panel",
				className,
			)}
			{...props}
		/>
	);
}

function Command({ className, ...props }: React.ComponentProps<typeof CommandBase>) {
	return <CommandBase className={cn("rounded-none! bg-transparent p-0 text-foreground", className)} {...props} />;
}

function CommandInput({
	className,
	children,
	...props
}: React.ComponentProps<typeof CommandInputBase> & { children?: React.ReactNode }) {
	return (
		<div className="flex h-[52px] shrink-0 items-center gap-2.5 border-b border-line pr-3.5 pl-4 *:data-[slot=command-input-wrapper]:min-w-0 *:data-[slot=command-input-wrapper]:flex-1 *:data-[slot=command-input-wrapper]:p-0 **:data-[slot=input-group]:h-auto! **:data-[slot=input-group]:border-0 **:data-[slot=input-group]:bg-transparent! **:data-[slot=input-group]:ring-0! **:data-[slot=input-group-addon]:pl-0! **:data-[slot=input-group-addon]:text-fg3 **:data-[slot=input-group-addon]:[&>svg]:opacity-100">
			<CommandInputBase
				className={cn("pl-2.5! text-[15px] text-foreground placeholder:text-fg4", className)}
				autoComplete="off"
				spellCheck={false}
				{...props}
			/>
			{children}
		</div>
	);
}

function CommandList({ className, ...props }: React.ComponentProps<typeof CommandListBase>) {
	return <CommandListBase className={cn("max-h-[min(420px,60vh)] p-1.5", className)} {...props} />;
}

function CommandGroup({ className, ...props }: React.ComponentProps<typeof CommandGroupBase>) {
	return (
		<CommandGroupBase
			className={cn(
				"p-0 **:[[cmdk-group-heading]]:px-2.5 **:[[cmdk-group-heading]]:pt-2 **:[[cmdk-group-heading]]:pb-1 **:[[cmdk-group-heading]]:text-[11.5px] **:[[cmdk-group-heading]]:font-normal **:[[cmdk-group-heading]]:text-fg3",
				className,
			)}
			{...props}
		/>
	);
}

function CommandItem({ className, ...props }: React.ComponentProps<typeof CommandItemBase>) {
	return (
		<CommandItemBase
			className={cn(
				"cursor-pointer data-[disabled=true]:pointer-events-auto data-[disabled=true]:cursor-not-allowed h-9 gap-2.5 rounded-[7px]! px-2.5 py-0 text-[13.5px] text-foreground data-selected:bg-panel dark:data-selected:bg-panel2 [&_svg:not([class*='size-'])]:size-[15px]",
				className,
			)}
			{...props}
		/>
	);
}

function CommandShortcut({ className, ...props }: React.ComponentProps<typeof CommandShortcutBase>) {
	return (
		<CommandShortcutBase
			className={cn(
				"flex min-w-0 items-center gap-2.5 tracking-normal text-fg3 group-data-selected/command-item:text-fg3",
				className,
			)}
			{...props}
		/>
	);
}

function CommandEmpty({ className, ...props }: React.ComponentProps<typeof CommandEmptyBase>) {
	return <CommandEmptyBase className={cn("px-3 py-7 text-[13px] text-fg3", className)} {...props} />;
}

function CommandFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			className={cn("flex shrink-0 items-center gap-4 border-t border-line px-4 py-2 text-xs text-fg3", className)}
			{...props}
		/>
	);
}

export {
	Command,
	CommandDialog,
	CommandEmpty,
	CommandFooter,
	CommandGroup,
	CommandInput,
	CommandItem,
	CommandList,
	CommandShortcut,
};
