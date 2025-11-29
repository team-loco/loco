import type { User } from "@/gen/user/v1/user_pb";
import type { ReactNode } from "react";
import { EventBell } from "./layout/EventBell";
import { SidebarTrigger } from "./ui/sidebar";

interface LayoutProps {
	children: ReactNode;
	user: User | null;
}

export function Layout({ children, user }: LayoutProps) {
	return (
		<div className="flex flex-col bg-background">
			{/* <Navbar user={user} /> */}
			<SidebarTrigger className="-ml-2" />
			<EventBell />
			<main className="flex-1 max-w-7xl w-full mx-auto px-6 py-8">
				{children}
			</main>
		</div>
	);
}
