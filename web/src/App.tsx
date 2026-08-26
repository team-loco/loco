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
const ResourceDetails = lazy(async () =>
	await import("@/pages/ResourceDetails").then((m) => ({ default: m.ResourceDetails })),
);
const ResourceSettings = lazy(async () =>
	await import("@/pages/ResourceSettings").then((m) => ({ default: m.ResourceSettings })),
);
const CreateResource = lazy(async () =>
	await import("@/pages/CreateResource").then((m) => ({ default: m.CreateResource })),
);
const Events = lazy(async () =>
	await import("@/pages/Events").then((m) => ({ default: m.Events })),
);
const Home = lazy(async () =>
	await import("@/pages/Home").then((m) => ({ default: m.Home })),
);
const Organizations = lazy(async () =>
	await import("@/pages/Organizations").then((m) => ({ default: m.Organizations })),
);
const OrgSettings = lazy(async () =>
	await import("@/pages/OrgSettings").then((m) => ({ default: m.OrgSettings })),
);
const Profile = lazy(async () =>
	await import("@/pages/Profile").then((m) => ({ default: m.Profile })),
);
const Team = lazy(async () =>
	await import("@/pages/Team").then((m) => ({ default: m.Team })),
);
const Tokens = lazy(async () =>
	await import("@/pages/Tokens").then((m) => ({ default: m.Tokens })),
);
const WorkspaceSettings = lazy(async () =>
	await import("@/pages/WorkspaceSettings").then((m) => ({ default: m.WorkspaceSettings })),
);
const Observability = lazy(async () =>
	await import("@/pages/Observability").then((m) => ({ default: m.Observability })),
);
const Resources = lazy(async () =>
	await import("@/pages/Resources").then((m) => ({ default: m.Resources })),
);
const Usage = lazy(async () =>
	await import("@/pages/Usage").then((m) => ({ default: m.Usage })),
);
const DashboardRedirect = lazy(async () =>
	await import("@/pages/DashboardRedirect").then((m) => ({ default: m.DashboardRedirect })),
);
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
