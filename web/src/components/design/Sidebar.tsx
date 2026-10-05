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
			style={{ "--sidebar-width": "16rem", "--sidebar-width-icon": "4rem", ...style } as React.CSSProperties}
			className={cn(
				"[&_[data-slot=sidebar-gap]]:duration-[160ms] [&_[data-slot=sidebar-gap]]:ease-[cubic-bezier(0.32,0.72,0,1)]",
				className
			)}
			{...props}
		/>
	)
}

function Sidebar({ className, ...props }: React.ComponentProps<typeof SidebarBase>) {
	return <SidebarBase className={cn(motion, className)} {...props} />
}

function SidebarGroupLabel({ className, ...props }: React.ComponentProps<typeof SidebarGroupLabelBase>) {
	return (
		<SidebarGroupLabelBase
			className={cn("h-[34px] rounded-sm px-2 pt-2.5 text-sm font-normal text-fg3 group-data-[collapsible=icon]:mt-0", className)}
			{...props}
		/>
	)
}

function SidebarMenuButton({ className, ...props }: React.ComponentProps<typeof SidebarMenuButtonBase>) {
	return (
		<SidebarMenuButtonBase
			className={cn(
				"rounded-sm px-2 text-md data-[size=lg]:rounded-lg data-[size=lg]:group-data-[collapsible=icon]:size-12! data-[size=lg]:group-data-[collapsible=icon]:p-2! data-active:font-medium [&_svg]:size-4",
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
