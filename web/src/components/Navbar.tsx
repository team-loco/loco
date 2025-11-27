import { useAuth } from "@/auth/AuthProvider";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
	NavigationMenu,
	NavigationMenuContent,
	NavigationMenuItem,
	NavigationMenuLink,
	NavigationMenuList,
	NavigationMenuTrigger,
	navigationMenuTriggerStyle,
} from "@/components/ui/navigation-menu";
import type { User } from "@/gen/user/v1/user_pb";
import { cn } from "@/lib/utils";

interface NavbarProps {
	user: User | null;
}

const ListItem = ({
	className,
	title,
	children,
	href,
	...props
}: React.ComponentPropsWithoutRef<"a"> & { title: string }) => (
	<li>
		<NavigationMenuLink asChild>
			<a
				href={href}
				className={cn(
					"block select-none space-y-1 rounded-base p-3 leading-none no-underline outline-none transition-colors hover:bg-background focus-visible:ring-2 focus-visible:ring-ring/50",
					className
				)}
				{...props}
			>
				<div className="text-sm font-heading leading-none">{title}</div>
				<p className="line-clamp-2 text-sm leading-snug text-foreground opacity-60">
					{children}
				</p>
			</a>
		</NavigationMenuLink>
	</li>
);

export function Navbar({ user }: NavbarProps) {
	const { logout } = useAuth();

	const handleLogout = () => {
		logout();
		window.location.href = "/";
	};

	return (
		<nav>
			<NavigationMenu className="w-full max-w-none justify-between px-6">
				{/* Logo */}
				<div className="flex items-center gap-3 shrink-0">
					<div className="w-8 h-8 bg-main rounded-neo flex items-center justify-center text-white font-heading text-sm">
						L
					</div>
					<h1 className="text-lg font-heading">Loco</h1>
				</div>

				{/* Navigation Items */}
				<NavigationMenuList className="flex-1 justify-center gap-8">
					<NavigationMenuItem>
						<NavigationMenuLink
							href="/"
							className={navigationMenuTriggerStyle()}
						>
							Home
						</NavigationMenuLink>
					</NavigationMenuItem>

					<div className="w-px bg-border opacity-40 h-6" />

					<NavigationMenuItem>
						<NavigationMenuTrigger>Apps</NavigationMenuTrigger>
						<NavigationMenuContent>
							<ul className="grid gap-3 p-4 w-[300px]">
								<ListItem href="/apps" title="All Apps">
									View and manage all your applications
								</ListItem>
								<ListItem href="/apps/new" title="Create App">
									Deploy a new application to Loco
								</ListItem>
								<ListItem href="/apps/templates" title="Templates">
									Start from pre-built application templates
								</ListItem>
							</ul>
						</NavigationMenuContent>
					</NavigationMenuItem>

					<div className="w-px bg-border opacity-40 h-6" />

					<NavigationMenuItem>
						<NavigationMenuLink
							href="https://docs.loco.dev"
							target="_blank"
							rel="noopener noreferrer"
							className={navigationMenuTriggerStyle()}
						>
							Docs
						</NavigationMenuLink>
					</NavigationMenuItem>

					<div className="w-px bg-border opacity-40 h-6" />

					<NavigationMenuItem>
						<NavigationMenuTrigger>Resources</NavigationMenuTrigger>
						<NavigationMenuContent>
							<ul className="grid gap-3 p-4 w-[300px]">
								<ListItem href="/observability" title="Observability">
									Monitor your applications and deployments
								</ListItem>
								<ListItem href="/settings" title="Settings">
									Configure your Loco workspace
								</ListItem>
								<ListItem
									href="https://docs.loco.dev/api"
									target="_blank"
									rel="noopener noreferrer"
									title="API Reference"
								>
									REST and gRPC API documentation
								</ListItem>
							</ul>
						</NavigationMenuContent>
					</NavigationMenuItem>
				</NavigationMenuList>

				{/* User Menu */}
				{user ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button
								variant="noShadow"
								className="flex items-center gap-2 shrink-0 border-2"
							>
								<div className="w-5 h-5 bg-main rounded-neo text-white flex items-center justify-center text-xs font-heading">
									{user.name.charAt(0).toUpperCase()}
								</div>
								<span className="text-sm">{user.name}</span>
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end">
							<DropdownMenuItem asChild>
								<a href="/profile">Profile</a>
							</DropdownMenuItem>
							<DropdownMenuItem asChild>
								<a href="/settings">Settings</a>
							</DropdownMenuItem>
							<DropdownMenuItem onClick={handleLogout}>Logout</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				) : (
					<Button className="shrink-0">Sign In</Button>
				)}
			</NavigationMenu>
		</nav>
	);
}
