import { useAuth } from "@/auth/AuthProvider";
import { getCurrentUser } from "@/gen/user/v1";
import type { ReactNode } from "react";
import { useEffect } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { Navbar } from "@/components/Navbar";
import { initializeMockEvents } from "@/lib/events";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const { isAuthenticated } = useAuth();
	const navigate = useNavigate();
	const {
		data: currentUserRes,
		isLoading,
		error,
	} = useQuery(getCurrentUser, {});

	// Initialize mock event streaming once
	useEffect(() => {
		initializeMockEvents();
	}, []);

	// Not authenticated - redirect to login
	if (!isAuthenticated) {
		navigate("/login");
		return null;
	}

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

	// Error fetching user
	if (error) {
		navigate("/login");
		return null;
	}

	const user = currentUserRes?.user ?? null;

	return (
		<div className="flex flex-col min-h-screen bg-background">
			<Navbar user={user} />
			<main className="flex-1 max-w-7xl w-full mx-auto px-6 py-8">
				{children}
			</main>
		</div>
	);
}
