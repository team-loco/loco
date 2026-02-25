import { AuthProvider } from "@/auth/AuthProvider";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { Toaster } from "@/components/ui/sonner";
import { HeaderProvider } from "@/context/HeaderContext";
import { ThemeProvider } from "@/lib/theme-provider";
import { ResourceDetails } from "@/pages/ResourceDetails";
import { ResourceSettings } from "@/pages/ResourceSettings";
import { CreateResource } from "@/pages/CreateResource";
import { Events } from "@/pages/Events";
import { Home } from "@/pages/Home";
import { Login } from "@/pages/Login";
import { OAuthCallback } from "@/pages/OAuthCallback";
import { Onboarding } from "@/pages/Onboarding";
import { Organizations } from "@/pages/Organizations";
import { OrgSettings } from "@/pages/OrgSettings";
import { Profile } from "@/pages/Profile";
import { Splash } from "@/pages/Splash";
import { Team } from "@/pages/Team";
import { Tokens } from "@/pages/Tokens";
import { WorkspaceSettings } from "@/pages/WorkspaceSettings";
import { Observability } from "@/pages/Observability";
import { Resources } from "@/pages/Resources";
import { Usage } from "@/pages/Usage";
import { DashboardRedirect } from "@/pages/DashboardRedirect";
import { TransportProvider } from "@connectrpc/connect-query";
import { createAsyncStoragePersister } from "@tanstack/query-async-storage-persister";
import { QueryClient } from "@tanstack/react-query";
import {
	PersistQueryClientProvider,
	type AsyncStorage,
} from "@tanstack/react-query-persist-client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { createTransport } from "./auth/connect-transport";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			refetchOnWindowFocus: false,
			retry: false,
			staleTime: 1000 * 60 * 60, // 1 hour - data is fresh for 1 hour
			gcTime: 1000 * 60 * 60 * 24, // 24 hours - keep cached data for 24 hours
		},
		mutations: {
			retry: false,
		},
	},
});

// Async wrapper around localStorage for the persister
const asyncLocalStorage: AsyncStorage = {
	getItem: (key: string) => Promise.resolve(localStorage.getItem(key)),
	setItem: (key: string, value: string) => {
		localStorage.setItem(key, value);
		return Promise.resolve();
	},
	removeItem: (key: string) => {
		localStorage.removeItem(key);
		return Promise.resolve();
	},
};

const persister = createAsyncStoragePersister({
	storage: asyncLocalStorage,
	key: "locoCache",
});

function AppRoutes() {
	return (
		<Routes>
			{/* Public routes */}
			<Route path="/" element={<Splash />} />
			<Route path="/login" element={<Login />} />
			<Route path="/oauth/callback" element={<OAuthCallback />} />
			<Route path="/onboarding" element={<Onboarding />} />

			{/* Protected routes - all under org/workspace structure */}
			<Route element={<ProtectedRoute />}>
				{/* Dashboard redirect to first workspace */}
				<Route path="/dashboard" element={<DashboardRedirect />} />

				{/* Org-level routes */}
				<Route path="/org/:orgId/settings" element={<OrgSettings />} />
				<Route path="/organizations" element={<Organizations />} />
				<Route path="/profile" element={<Profile />} />
				<Route path="/team" element={<Team />} />
				<Route path="/tokens" element={<Tokens />} />

				{/* Workspace-scoped routes */}
				<Route path="/org/:orgId/wks/:workspaceId">
					<Route path="" element={<Home />} />
					<Route path="dashboard" element={<Home />} />
					<Route path="resources" element={<Resources />} />
					<Route path="resource/:resourceId" element={<ResourceDetails />} />
					<Route path="resource/:resourceId/settings" element={<ResourceSettings />} />
					<Route path="create-resource" element={<CreateResource />} />
					<Route path="events" element={<Events />} />
					<Route path="observability" element={<Observability />} />
					<Route path="usage" element={<Usage />} />
					<Route path="settings" element={<WorkspaceSettings />} />
				</Route>
			</Route>

			{/* Catch-all - redirect to workspace if authenticated, else to splash */}
			<Route path="*" element={<Navigate to="/" />} />
		</Routes>
	);
}

export default function App() {
	return (
		<ThemeProvider>
			<BrowserRouter>
				<TransportProvider transport={createTransport()}>
					<PersistQueryClientProvider
						client={queryClient}
						persistOptions={{ persister }}
					>
						<AuthProvider>
							<HeaderProvider>
								<Toaster />
								<AppRoutes />
							</HeaderProvider>
						</AuthProvider>
					</PersistQueryClientProvider>
				</TransportProvider>
			</BrowserRouter>
		</ThemeProvider>
	);
}
