import { AuthProvider, useAuth } from "@/auth/AuthProvider";
import { Layout } from "@/components/Layout";
import { getCurrentUser } from "@/gen/user/v1";
import { Home } from "@/pages/Home";
import { Login } from "@/pages/Login";
import { OAuthCallback } from "@/pages/OAuthCallback";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Route, Routes } from "react-router";
import { createTransport } from "./auth/connect-transport";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			refetchOnWindowFocus: false,
			retry: false,
		},
	},
});

function Landing() {
	const { isAuthenticated } = useAuth();
	const {
		data: currentUserRes,
		isLoading,
		error,
	} = useQuery(getCurrentUser, {});

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
		return <Login />;
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
		<BrowserRouter>
			<AuthProvider>
				<TransportProvider transport={createTransport()}>
					<QueryClientProvider client={queryClient}>
						<Routes>
							<Route path="/login" element={<Login />} />
							<Route path="/oauth/callback" element={<OAuthCallback />} />
							<Route path="/" element={<Landing />} />
						</Routes>
					</QueryClientProvider>
				</TransportProvider>
			</AuthProvider>
		</BrowserRouter>
	);
}
