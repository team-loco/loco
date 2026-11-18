import {
	Command as CommandBase,
	CommandDialog as CommandDialogBase,
	CommandEmpty as CommandEmptyBase,
	CommandGroup as CommandGroupBase,
	CommandItem as CommandItemBase,
	CommandList as CommandListBase,
	CommandShortcut as CommandShortcutBase,
} from "@/components/ui/command";
import { Command as CommandPrimitive } from "cmdk";
import { SearchIcon } from "lucide-react";

import { cn } from "@/lib/utils";

function CommandDialog({ className, ...props }: React.ComponentProps<typeof CommandDialogBase>) {
	return (
		<CommandDialogBase
			className={cn(
				"cmd-palette top-[14vh] flex max-h-[calc(100dvh-32px)] w-[620px] max-w-[calc(100%-32px)] flex-col gap-0 rounded-[12px]! border border-line2 bg-background p-0 text-base shadow-[0_24px_64px_-12px_rgba(20,20,20,0.3)] ring-0 sm:max-w-[calc(100%-32px)] dark:bg-panel",
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
	leading,
	...props
}: React.ComponentProps<typeof CommandPrimitive.Input> & {
	children?: React.ReactNode;
	leading?: React.ReactNode;
}) {
	return (
		<div className="flex h-[52px] shrink-0 items-center gap-2.5 border-b border-line pr-3.5 pl-4">
			<SearchIcon aria-hidden className="size-3.5 shrink-0 text-fg3" />
			{leading !== undefined && <span className="flex">{leading}</span>}
			<CommandPrimitive.Input
				data-slot="command-input"
				className={cn(
					"min-w-0 flex-1 bg-transparent text-[15px] text-foreground outline-hidden placeholder:text-fg3 disabled:opacity-50",
					className,
				)}
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
