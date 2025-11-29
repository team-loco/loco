import type { User } from "@/gen/user/v1/user_pb";
import type { ReactNode } from "react";
import { EventBell } from "./layout/EventBell";
import { SidebarTrigger } from "./ui/sidebar";

interface LayoutProps {
	children: ReactNode;
	user?: User | null;
	header?: ReactNode;
}

export function Layout({ children, header }: LayoutProps) {
	return (
		<div className="flex flex-col bg-white">
			{/* <Navbar user={user} /> */}
			<div className="flex items-start justify-between gap-4 px-6 py-4">
				<div className="flex-1">{header}</div>
				<div className="flex items-center gap-2">
					<EventBell />
					<SidebarTrigger className="-ml-2" />
				</div>
			</div>
			<main className="flex-1 max-w-7xl w-full mx-auto px-6 py-8">
				{children}
			</main>
		</div>
	);
}
