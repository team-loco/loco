import { useAuth } from "@/auth/AuthProvider";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { User } from "@/gen/user/v1/user_pb";
import { useNavigate } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { BreadcrumbNav } from "./layout/Breadcrumb";
import { EventBell } from "./layout/EventBell";
import { NavMenu } from "./layout/NavMenu";

interface NavbarProps {
	user: User | null;
}

export function Navbar({ user }: NavbarProps) {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs.length > 0 ? orgs[0].id : null;

	const handleLogout = () => {
		logout();
		window.location.href = "/";
	};

	return (
		<nav className="border-b-2 border-border bg-background">
			<div className="flex items-center justify-between px-6 py-4 max-w-full gap-4">
				{/* Left: Logo */}
				<button
					onClick={() => navigate("/")}
					className="flex items-center gap-3 shrink-0 hover:opacity-80 transition-opacity"
				>
					<div className="w-8 h-8 bg-main rounded-neo flex items-center justify-center text-white font-heading text-sm font-bold">
						L
					</div>
					<h1 className="text-lg font-heading font-bold hidden sm:block">
						Loco
					</h1>
				</button>

				{/* Center: Navigation Menu */}
				<NavMenu />

				{/* Center-Right: Breadcrumb */}
				<div className="flex-1 hidden md:block min-w-0">
					<BreadcrumbNav />
				</div>

				{/* Right: Bell, User Menu */}
				<div className="flex items-center gap-4 shrink-0">
					<EventBell />

					{user ? (
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button
									variant="noShadow"
									className="flex items-center gap-2 border-0"
								>
									<div className="w-5 h-5 bg-main rounded-neo text-white flex items-center justify-center text-xs font-heading font-bold">
										{user.name.charAt(0).toUpperCase()}
									</div>
									<span className="text-sm hidden sm:inline">{user.name}</span>
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<DropdownMenuItem asChild>
									<button
										onClick={() => navigate("/profile")}
										className="w-full text-left"
									>
										Profile Settings
									</button>
								</DropdownMenuItem>
								{firstOrgId && (
									<>
										<DropdownMenuSeparator />
										<DropdownMenuItem asChild>
											<button
												onClick={() => navigate(`/org/${firstOrgId}/settings`)}
												className="w-full text-left"
											>
												Organization Settings
											</button>
										</DropdownMenuItem>
									</>
								)}
								<DropdownMenuSeparator />
								<DropdownMenuItem asChild>
									<a
										href="https://docs.loco.dev"
										target="_blank"
										rel="noopener noreferrer"
									>
										Documentation
									</a>
								</DropdownMenuItem>
								<DropdownMenuItem onClick={handleLogout}>
									Logout
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					) : null}
				</div>
			</div>
		</nav>
	);
}
