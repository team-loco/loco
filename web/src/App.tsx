import { AuthProvider } from "@/auth/AuthProvider";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { Toaster } from "@/components/ui/sonner";
import { HeaderProvider } from "@/context/HeaderContext";
import { ThemeProvider } from "@/lib/theme-provider";
import { AppDetails } from "@/pages/AppDetails";
import { AppSettings } from "@/pages/AppSettings";
import { CreateApp } from "@/pages/CreateApp";
import { Events } from "@/pages/Events";
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
		mutations: {
			retry: false,
		},
	},
});

export default function App() {
	return (
		<ThemeProvider>
			<BrowserRouter>
				<AuthProvider>
					<HeaderProvider>
						<TransportProvider transport={createTransport()}>
							<QueryClientProvider client={queryClient}>
								<Toaster />
								<Routes>
									{/* Public routes */}
									<Route path="/login" element={<Login />} />
									<Route path="/oauth/callback" element={<OAuthCallback />} />
									<Route path="/onboarding" element={<Onboarding />} />

									{/* Protected routes */}
									<Route element={<ProtectedRoute />}>
										<Route path="/dashboard" element={<Home />} />
										<Route path="/app/:appId" element={<AppDetails />} />
										<Route
											path="/app/:appId/settings"
											element={<AppSettings />}
										/>
										<Route path="/create-app" element={<CreateApp />} />
										<Route path="/events" element={<Events />} />
										<Route path="/profile" element={<Profile />} />
										<Route
											path="/org/:orgId/settings"
											element={<OrgSettings />}
										/>
										<Route
											path="/workspace/:workspaceId/settings"
											element={<WorkspaceSettings />}
										/>
									</Route>

									{/* Default redirect */}
									<Route path="/" element={<Navigate to="/dashboard" />} />
									<Route path="*" element={<Navigate to="/dashboard" />} />
								</Routes>
							</QueryClientProvider>
						</TransportProvider>
					</HeaderProvider>
				</AuthProvider>
			</BrowserRouter>
		</ThemeProvider>
	);
}
