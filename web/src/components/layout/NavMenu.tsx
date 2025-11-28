import { useNavigate } from "react-router";
import {
	NavigationMenu,
	NavigationMenuContent,
	NavigationMenuItem,
	NavigationMenuLink,
	NavigationMenuList,
	NavigationMenuTrigger,
	navigationMenuTriggerStyle,
} from "@/components/ui/navigation-menu";
import { cn } from "@/lib/utils";

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

export function NavMenu() {
	const navigate = useNavigate();

	return (
		<NavigationMenu className="hidden md:flex">
			<NavigationMenuList className="gap-2">
				<NavigationMenuItem>
					<NavigationMenuLink
						onClick={() => navigate("/dashboard")}
						className={cn(
							navigationMenuTriggerStyle(),
							"cursor-pointer"
						)}
					>
						Dashboard
					</NavigationMenuLink>
				</NavigationMenuItem>

				<NavigationMenuItem>
					<NavigationMenuTrigger>Apps</NavigationMenuTrigger>
					<NavigationMenuContent>
						<ul className="grid gap-3 p-4 w-[300px]">
							<ListItem href="/dashboard" title="All Apps">
								View and manage all your applications
							</ListItem>
							<ListItem href="/create-app" title="Create App">
								Deploy a new application to Loco
							</ListItem>
						</ul>
					</NavigationMenuContent>
				</NavigationMenuItem>

				<NavigationMenuItem>
					<NavigationMenuTrigger>Resources</NavigationMenuTrigger>
					<NavigationMenuContent>
						<ul className="grid gap-3 p-4 w-[300px]">
							<ListItem href="/profile" title="Profile">
								Manage your profile and settings
							</ListItem>
							<ListItem
								href="https://docs.loco.dev"
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
		</NavigationMenu>
	);
}
