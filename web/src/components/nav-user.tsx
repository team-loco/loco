import {
	BadgeCheck,
	Bell,
	ChevronsUpDown,
	CreditCard,
	LogOut,
	Check,
	HelpCircle,
} from "lucide-react";
import { useNavigate } from "react-router";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	useSidebar,
} from "@/components/ui/sidebar";
import { useAuth } from "@/auth/AuthProvider";
import { toast } from "sonner";

export interface Workspace {
	id: bigint;
	name: string;
}

export function NavUser({
	user,
	workspaces = [],
	activeWorkspaceId,
}: {
	user: {
		name: string;
		email: string;
		avatar: string;
	};
	workspaces?: Workspace[];
	activeWorkspaceId?: bigint | null;
}) {
	const { isMobile } = useSidebar();
	const navigate = useNavigate();
	const { logout } = useAuth();

	return (
		<SidebarMenu>
			<SidebarMenuItem>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<SidebarMenuButton
							size="lg"
							className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground cursor-pointer"
						>
							<Avatar className="h-8 w-8 rounded-lg">
								<AvatarImage src={user.avatar} alt={user.name} />
								<AvatarFallback className="rounded-lg">CN</AvatarFallback>
							</Avatar>
							<div className="grid flex-1 text-left text-sm leading-tight">
								<span className="truncate font-semibold">{user.name}</span>
								<span className="truncate text-xs">{user.email}</span>
							</div>
							<ChevronsUpDown className="ml-auto size-4" />
						</SidebarMenuButton>
					</DropdownMenuTrigger>
					<DropdownMenuContent
						className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-lg"
						side={isMobile ? "bottom" : "right"}
						align="end"
						sideOffset={4}
					>
						<DropdownMenuLabel className="p-0 font-normal">
							<div className="px-1 py-1.5 text-left text-sm">
								<span className="truncate font-bold">{user.name}</span>
							</div>
						</DropdownMenuLabel>
						<DropdownMenuSeparator />

						{workspaces.length > 0 && (
							<>
								<DropdownMenuLabel className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
									Workspaces
								</DropdownMenuLabel>
								<DropdownMenuGroup>
									{workspaces.map((workspace) => (
										<DropdownMenuItem
											key={workspace.id.toString()}
											onClick={() =>
												navigate(`/dashboard?workspace=${workspace.id}`)
											}
											className="flex items-center justify-between"
										>
											<span>{workspace.name}</span>
											{activeWorkspaceId === workspace.id && (
												<Check className="h-4 w-4" />
											)}
										</DropdownMenuItem>
									))}
								</DropdownMenuGroup>
								<DropdownMenuSeparator />
							</>
						)}

						<DropdownMenuGroup>
							<DropdownMenuItem onClick={() => navigate("/profile")}>
								<BadgeCheck />
								Account
							</DropdownMenuItem>
							<DropdownMenuItem>
								<CreditCard />
								Billing
							</DropdownMenuItem>
							<DropdownMenuItem>
								<Bell />
								Notifications
							</DropdownMenuItem>
							<DropdownMenuItem>
								<HelpCircle />
								Support
							</DropdownMenuItem>
						</DropdownMenuGroup>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							onClick={async () => {
								try {
									await logout();
									navigate("/");
									toast.success("Logged out successfully");
								} catch (error) {
									console.error("Logout failed:", error);
									toast.error("Failed to logout");
								}
							}}
						>
							<LogOut />
							Log out
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</SidebarMenuItem>
		</SidebarMenu>
	);
}
