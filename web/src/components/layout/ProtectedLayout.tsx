import { useAuth } from "@/auth/AuthProvider";
import { AppSidebar } from "@/components/layout/AppSidebar";
import { SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { getCurrentUser } from "@/gen/user/v1";
import { initializeMockEvents } from "@/lib/events";
import { useQuery } from "@connectrpc/connect-query";
import type { ReactNode } from "react";
import { useEffect } from "react";
import { useNavigate } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const navigate = useNavigate();
	const { logout } = useAuth();
	const { isLoading, error } = useQuery(getCurrentUser, {});

	// Handle auth failures by redirecting to login
	useEffect(() => {
		if (error) {
			logout();
			navigate("/login", { replace: true });
		}
	}, [error, logout, navigate]);

	// todo: remove this;Initialize mock event streaming once
	useEffect(() => {
		initializeMockEvents();
	}, []);

	// Loading user data
	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-screen bg-background">
				<div className="text-center">
					<div className="w-8 h-8 bg-main rounded-neo mx-auto mb-4 animate-pulse"></div>
					<p className="text-foreground font-base">Loading Loco...</p>
				</div>
			</div>
		);
	}

	return (
		<SidebarProvider>
			<div className="flex min-h-screen bg-background w-full">
				<AppSidebar />
				<div className="flex-1 flex flex-col">
					<div className="p-4">
						<SidebarTrigger className="-ml-2" />
					</div>
					<main className="flex-1 w-full px-6 py-8">{children}</main>
				</div>
			</div>
		</SidebarProvider>
	);
}
