import {
	Sidebar as SidebarBase,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupLabel as SidebarGroupLabelBase,
	SidebarHeader,
	SidebarInset as SidebarInsetBase,
	SidebarMenu,
	SidebarMenuButton as SidebarMenuButtonBase,
	SidebarMenuItem,
	SidebarProvider as SidebarProviderBase,
	useSidebar,
} from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

const motion = "duration-[220ms] ease-[cubic-bezier(0.2,0.7,0.2,1)]";

function SidebarProvider({ style, className, ...props }: React.ComponentProps<typeof SidebarProviderBase>) {
	return (
		<SidebarProviderBase
			style={{ "--sidebar-width": "232px", ...style } as React.CSSProperties}
			className={cn(
				"[&_[data-slot=sidebar-gap]]:duration-[220ms] [&_[data-slot=sidebar-gap]]:ease-[cubic-bezier(0.2,0.7,0.2,1)]",
				className,
			)}
			{...props}
		/>
	);
}

function Sidebar({ className, ...props }: React.ComponentProps<typeof SidebarBase>) {
	return (
		<SidebarBase variant="inset" collapsible="offcanvas" className={cn(motion, "z-30 p-0", className)} {...props} />
	);
}

function SidebarInset({ className, ...props }: React.ComponentProps<typeof SidebarInsetBase>) {
	return (
		<SidebarInsetBase
			className={cn(
				"min-w-0 bg-background transition-[margin] md:peer-data-[variant=inset]:rounded-[12px] md:peer-data-[variant=inset]:border md:peer-data-[variant=inset]:border-line",
				motion,
				className,
			)}
			{...props}
		/>
	);
}

function SidebarGroupLabel({ className, ...props }: React.ComponentProps<typeof SidebarGroupLabelBase>) {
	return (
		<SidebarGroupLabelBase
			className={cn("h-auto rounded-sm px-2.5 pt-[18px] pb-1.5 text-[12px] font-normal text-fg3", className)}
			{...props}
		/>
	);
}

function SidebarMenuButton({ className, ...props }: React.ComponentProps<typeof SidebarMenuButtonBase>) {
	return (
		<SidebarMenuButtonBase
			className={cn(
				"h-8 gap-2.5 rounded-[5px] px-2.5 text-[13.5px] text-fg2 transition-colors duration-150 ease-out hover:bg-shell-hover hover:text-fg2 active:bg-shell-hover active:text-fg2 data-active:bg-shell-active data-active:font-medium data-active:text-foreground data-active:hover:bg-shell-active data-active:hover:text-foreground [&_svg]:size-4 [&_svg]:text-fg3 data-active:[&_svg]:text-foreground",
				className,
			)}
			{...props}
		/>
	);
}

export {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarInset,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarProvider,
	useSidebar,
};
