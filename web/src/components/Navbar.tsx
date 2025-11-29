import { SidebarTrigger } from "@/components/ui/sidebar";

export function Navbar() {
	return (
		<nav className="border-b-2 border-border bg-background h-14 flex items-center gap-4 px-4">
			{/* Sidebar toggle on mobile */}
			<SidebarTrigger className="-ml-2" />
		</nav>
	);
}
