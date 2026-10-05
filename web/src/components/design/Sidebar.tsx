import {
	Sidebar,
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

function SidebarProvider({ style, ...props }: React.ComponentProps<typeof SidebarProviderBase>) {
	return (
		<SidebarProviderBase
			style={{ "--sidebar-width": "16rem", "--sidebar-width-icon": "4rem", ...style } as React.CSSProperties}
			{...props}
		/>
	)
}

function SidebarGroupLabel({ className, ...props }: React.ComponentProps<typeof SidebarGroupLabelBase>) {
	return (
		<SidebarGroupLabelBase
			className={cn("h-[34px] rounded-sm px-2 pt-2.5 text-sm font-normal text-fg3", className)}
			{...props}
		/>
	)
}

function SidebarMenuButton({ className, ...props }: React.ComponentProps<typeof SidebarMenuButtonBase>) {
	return (
		<SidebarMenuButtonBase
			className={cn(
				"rounded-sm px-2 text-md data-[size=lg]:rounded-lg data-active:font-medium [&_svg]:size-4",
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
