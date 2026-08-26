import { AuthProvider } from "@/auth/AuthProvider";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { Toaster } from "@/components/ui/sonner";
import { HeaderProvider } from "@/context/HeaderContext";
import { ThemeProvider } from "@/lib/theme-provider";
import { Login } from "@/pages/Login";
import { OAuthCallback } from "@/pages/OAuthCallback";
import { Onboarding } from "@/pages/Onboarding";
import { Splash } from "@/pages/Splash";
import { TransportProvider } from "@connectrpc/connect-query";
import { createAsyncStoragePersister } from "@tanstack/query-async-storage-persister";
import { QueryClient } from "@tanstack/react-query";
import {
	PersistQueryClientProvider,
	type AsyncStorage,
} from "@tanstack/react-query-persist-client";
import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";

// Heavy pages — loaded only when the route is visited
const ResourceDetails = lazy(async () => {
	const m = await import("@/pages/ResourceDetails");
	return { default: m.ResourceDetails };
});
const ResourceSettings = lazy(async () => {
	const m = await import("@/pages/ResourceSettings");
	return { default: m.ResourceSettings };
});
const CreateResource = lazy(async () => {
	const m = await import("@/pages/CreateResource");
	return { default: m.CreateResource };
});
const Events = lazy(async () => {
	const m = await import("@/pages/Events");
	return { default: m.Events };
});
const Home = lazy(async () => {
	const m = await import("@/pages/Home");
	return { default: m.Home };
});
const Organizations = lazy(async () => {
	const m = await import("@/pages/Organizations");
	return { default: m.Organizations };
});
const OrgSettings = lazy(async () => {
	const m = await import("@/pages/OrgSettings");
	return { default: m.OrgSettings };
});
const Profile = lazy(async () => {
	const m = await import("@/pages/Profile");
	return { default: m.Profile };
});
const Team = lazy(async () => {
	const m = await import("@/pages/Team");
	return { default: m.Team };
});
const Tokens = lazy(async () => {
	const m = await import("@/pages/Tokens");
	return { default: m.Tokens };
});
const WorkspaceSettings = lazy(async () => {
	const m = await import("@/pages/WorkspaceSettings");
	return { default: m.WorkspaceSettings };
});
const Observability = lazy(async () => {
	const m = await import("@/pages/Observability");
	return { default: m.Observability };
});
const Resources = lazy(async () => {
	const m = await import("@/pages/Resources");
	return { default: m.Resources };
});
const Usage = lazy(async () => {
	const m = await import("@/pages/Usage");
	return { default: m.Usage };
});
const DashboardRedirect = lazy(async () => {
	const m = await import("@/pages/DashboardRedirect");
	return { default: m.DashboardRedirect };
});
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
	getItem: async (key: string) => await Promise.resolve(localStorage.getItem(key)),
	setItem: async (key: string, value: string) => {
		localStorage.setItem(key, value);
		await Promise.resolve();
	},
	removeItem: async (key: string) => {
		localStorage.removeItem(key);
		await Promise.resolve();
	},
};

const persister = createAsyncStoragePersister({
	storage: asyncLocalStorage,
	key: "locoCache",
});

function AppRoutes() {
	return (
		<Suspense>
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
		</Suspense>
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
