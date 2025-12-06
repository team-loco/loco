import { useAuth } from "@/auth/AuthProvider";
import { AppSidebar } from "@/components/layout/AppSidebar";
import { SiteHeader } from "@/components/site-header";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { getCurrentUser } from "@/gen/user/v1";
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

	// Loading user data
	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-screen bg-background">
				<div className="text-center">
					<div className="w-8 h-8 bg-main rounded-lg mx-auto mb-4 animate-pulse"></div>
					<p className="text-foreground font-base">Loading Loco...</p>
				</div>
			</div>
		);
	}

	return (
		<SidebarProvider className="flex flex-col w-full min-h-screen">
			<SiteHeader />
			<div className="flex flex-1">
				<AppSidebar />
				<SidebarInset className="flex flex-col flex-1">
					<main className="flex-1 w-full overflow-auto px-4 py-4 flex justify-center">
						<div className="w-[85%] mx-auto">{children}</div>
					</main>
				</SidebarInset>
			</div>
		</SidebarProvider>
	);
}
