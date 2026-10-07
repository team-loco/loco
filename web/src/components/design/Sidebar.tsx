import {
	Sidebar as SidebarBase,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupAction,
	SidebarGroupContent,
	SidebarGroupLabel as SidebarGroupLabelBase,
	SidebarHeader,
	SidebarInput,
	SidebarInset,
	SidebarMenu,
	SidebarMenuAction,
	SidebarMenuBadge,
	SidebarMenuButton as SidebarMenuButtonBase,
	SidebarMenuItem,
	SidebarMenuSkeleton,
	SidebarMenuSub,
	SidebarMenuSubButton,
	SidebarMenuSubItem,
	SidebarProvider as SidebarProviderBase,
	SidebarRail,
	SidebarSeparator,
	SidebarTrigger,
} from "@/components/ui/sidebar"
import { useSidebar } from "@/components/ui/use-sidebar"
import { cn } from "@/lib/utils"

const motion = "duration-[160ms] ease-[cubic-bezier(0.32,0.72,0,1)]"

function SidebarProvider({ style, className, ...props }: React.ComponentProps<typeof SidebarProviderBase>) {
	return (
		<SidebarProviderBase
			style={{ "--sidebar-width": "236px", "--sidebar-width-icon": "4rem", ...style } as React.CSSProperties}
			className={cn(
				"[&_[data-slot=sidebar-gap]]:duration-[160ms] [&_[data-slot=sidebar-gap]]:ease-[cubic-bezier(0.32,0.72,0,1)]",
				className
			)}
			{...props}
		/>
	)
}

function Sidebar({ className, ...props }: React.ComponentProps<typeof SidebarBase>) {
	return <SidebarBase className={cn(motion, "z-30", className)} {...props} />
}

function SidebarGroupLabel({ className, ...props }: React.ComponentProps<typeof SidebarGroupLabelBase>) {
	return (
		<SidebarGroupLabelBase
			className={cn("h-auto rounded-sm px-2 pt-5 pb-1.5 text-[11.5px] font-medium text-fg3 group-data-[collapsible=icon]:mt-0", className)}
			{...props}
		/>
	)
}

function SidebarMenuButton({ className, ...props }: React.ComponentProps<typeof SidebarMenuButtonBase>) {
	return (
		<SidebarMenuButtonBase
			className={cn(
				"h-[30px] gap-2.5 rounded-sm px-2 text-[13.5px] text-fg2 transition-[width,height,padding,color,background-color] duration-150 ease-out hover:bg-bg2 hover:text-fg2 active:bg-bg2 active:text-fg2 data-[size=lg]:rounded-lg data-[size=lg]:group-data-[collapsible=icon]:size-12! data-[size=lg]:group-data-[collapsible=icon]:p-2! data-active:bg-bg3 data-active:font-semibold data-active:text-foreground data-active:hover:bg-bg3 data-active:hover:text-foreground [&_svg]:size-4 [&_svg]:text-fg3 data-active:[&_svg]:text-foreground",
				className
			)}
			{...props}
		/>
	)
}

export {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupAction,
	SidebarGroupContent,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarInput,
	SidebarInset,
	SidebarMenu,
	SidebarMenuAction,
	SidebarMenuBadge,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarMenuSkeleton,
	SidebarMenuSub,
	SidebarMenuSubButton,
	SidebarMenuSubItem,
	SidebarProvider,
	SidebarRail,
	SidebarSeparator,
	SidebarTrigger,
	useSidebar,
}
