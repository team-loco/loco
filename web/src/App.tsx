import { AuthProvider, useAuth } from "@/auth/AuthProvider";
import { Layout } from "@/components/Layout";
import { Home } from "@/pages/Home";
import { Login } from "@/pages/Login";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createTransport } from "./auth/connect-transport";
import { getCurrentUser } from "@/gen/user/v1";
const queryClient = new QueryClient();

function AppContent() {
	const { isAuthenticated } = useAuth();
	const { data: currentUserRes, isLoading, error } = useQuery(getCurrentUser, {});

	// If not authenticated, show login
	if (!isAuthenticated) {
		return <Login />;
	}

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

	if (error) {
		return (
			<div className="flex items-center justify-center min-h-screen bg-background">
				<div className="text-center max-w-md px-6">
					<p className="text-destructive font-heading mb-4">
						Error loading user
					</p>
					<p className="text-sm text-foreground opacity-70">{error.message}</p>
					<p className="text-xs text-foreground opacity-50 mt-4">
						Make sure the backend is running on http://localhost:8000 and your
						token is valid
					</p>
				</div>
			</div>
		);
	}

	const user = currentUserRes?.user ?? null;

	return (
		<Layout user={user}>
			<Home />
		</Layout>
	);
}

export default function App() {
	return (
		<AuthProvider>
			<TransportProvider transport={createTransport()}>
				<QueryClientProvider client={queryClient}>
					<AppContent />
				</QueryClientProvider>
			</TransportProvider>
		</AuthProvider>
	);
}
