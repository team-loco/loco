import { AuthProvider } from "@/auth/AuthProvider";
import { ProtectedLayout } from "@/components/layout/ProtectedLayout";
import { Toaster } from "@/components/ui/sonner";
import { AppDetails } from "@/pages/AppDetails";
import { AppSettings } from "@/pages/AppSettings";
import { CreateApp } from "@/pages/CreateApp";
import { Home } from "@/pages/Home";
import { Login } from "@/pages/Login";
import { OAuthCallback } from "@/pages/OAuthCallback";
import { Onboarding } from "@/pages/Onboarding";
import { OrgSettings } from "@/pages/OrgSettings";
import { Profile } from "@/pages/Profile";
import { WorkspaceSettings } from "@/pages/WorkspaceSettings";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { createTransport } from "./auth/connect-transport";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			refetchOnWindowFocus: false,
			retry: false,
		},
	},
});

export default function App() {
	return (
		<BrowserRouter>
			<AuthProvider>
				<TransportProvider transport={createTransport()}>
					<QueryClientProvider client={queryClient}>
						<Toaster />
						<Routes>
							{/* Public routes */}
							<Route path="/login" element={<Login />} />
							<Route path="/oauth/callback" element={<OAuthCallback />} />
							<Route path="/onboarding" element={<Onboarding />} />

							{/* Protected routes */}
							<Route
								path="/dashboard"
								element={
									<ProtectedLayout>
										<Home />
									</ProtectedLayout>
								}
							/>

							{/* App routes */}
							<Route
								path="/app/:appId"
								element={
									<ProtectedLayout>
										<AppDetails />
									</ProtectedLayout>
								}
							/>
							<Route
								path="/app/:appId/settings"
								element={
									<ProtectedLayout>
										<AppSettings />
									</ProtectedLayout>
								}
							/>
							<Route
								path="/create-app"
								element={
									<ProtectedLayout>
										<CreateApp />
									</ProtectedLayout>
								}
							/>

							{/* Settings pages */}
							<Route
								path="/profile"
								element={
									<ProtectedLayout>
										<Profile />
									</ProtectedLayout>
								}
							/>
							<Route
								path="/org/:orgId/settings"
								element={
									<ProtectedLayout>
										<OrgSettings />
									</ProtectedLayout>
								}
							/>
							<Route
								path="/workspace/:workspaceId/settings"
								element={
									<ProtectedLayout>
										<WorkspaceSettings />
									</ProtectedLayout>
								}
							/>

							{/* Default redirect */}
							<Route path="/" element={<Navigate to="/dashboard" />} />
							<Route path="*" element={<Navigate to="/dashboard" />} />
						</Routes>
					</QueryClientProvider>
				</TransportProvider>
			</AuthProvider>
		</BrowserRouter>
	);
}
