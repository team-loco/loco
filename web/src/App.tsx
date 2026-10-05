import { AuthProvider } from "@/auth/AuthProvider";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { Toaster } from "@/components/design/Sonner";
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
import { AppLoaderProvider, AppLoading } from "@/context/AppLoader";
import { BrowserRouter, Navigate, Route, Routes, useParams } from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspacePath } from "@/lib/routes";
import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

import { pageImporters } from "@/lib/lazy-pages";

const Dashboard = lazy(async () => ({ default: (await pageImporters.Dashboard()).Dashboard }));
const Resource = lazy(async () => ({ default: (await pageImporters.Resource()).Resource }));
const Observability = lazy(async () => ({ default: (await pageImporters.Observability()).Observability }));
const Settings = lazy(async () => ({ default: (await pageImporters.Settings()).Settings }));
const Team = lazy(async () => ({ default: (await pageImporters.Team()).Team }));
const Tokens = lazy(async () => ({ default: (await pageImporters.Tokens()).Tokens }));
const Organizations = lazy(async () => ({ default: (await pageImporters.Organizations()).Organizations }));
const Profile = lazy(async () => ({ default: (await pageImporters.Profile()).Profile }));
const DashboardRedirect = lazy(async () => ({ default: (await pageImporters.DashboardRedirect()).DashboardRedirect }));

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

const asyncLocalStorage: AsyncStorage = {
	getItem: async (key: string) => await Promise.resolve(readStorage(key)),
	setItem: async (key: string, value: string) => {
		writeStorage(key, value);
		await Promise.resolve();
	},
	removeItem: async (key: string) => {
		removeStorage(key);
		await Promise.resolve();
	},
};

const persister = createAsyncStoragePersister({
	storage: asyncLocalStorage,
	key: "loco:query-cache:v1",
});

const transport = createTransport();

function OrgRedirect({ to }: { to: "team" | "settings" }) {
	const { orgId } = useParams();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const org = orgId ?? activeOrgId;
	if (org === null) return <Navigate to="/organizations" replace />;
	if (to === "team") return <Navigate to={`/org/${org}/team`} replace />;
	const ws = workspaces.find((w) => w.orgId === org)?.id ?? activeWorkspaceId;
	if (ws === null) return <Navigate to="/organizations" replace />;
	return <Navigate to={`${workspacePath(org, ws, "settings")}?tab=org`} replace />;
}

function AppRoutes() {
	return (
		<Suspense fallback={<AppLoading />}>
			<Routes>
				{/* Public routes */}
				<Route path="/" element={<Splash />} />
				<Route path="/login" element={<Login />} />
				<Route path="/oauth/callback" element={<OAuthCallback />} />
				<Route path="/onboarding" element={<Onboarding />} />

				<Route element={<ProtectedRoute />}>
					<Route path="/dashboard" element={<DashboardRedirect />} />
					<Route path="/organizations" element={<Organizations />} />
					<Route path="/profile" element={<Profile />} />
					<Route path="/tokens" element={<Tokens />} />
					<Route path="/team" element={<OrgRedirect to="team" />} />
					<Route path="/org/:orgId/team" element={<Team />} />
					<Route path="/org/:orgId/settings" element={<OrgRedirect to="settings" />} />

					<Route path="/org/:orgId/wks/:workspaceId">
						<Route path="" element={<Dashboard />} />
						<Route path="dashboard" element={<Navigate to=".." relative="path" replace />} />
						<Route path="resource/:resourceId" element={<Resource />} />
						<Route path="observability" element={<Observability />} />
						<Route path="events" element={<Navigate to="../observability?view=events" relative="path" replace />} />
						<Route path="settings" element={<Settings />} />
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
				<TransportProvider transport={transport}>
					<PersistQueryClientProvider
						client={queryClient}
						persistOptions={{ persister }}
					>
						<AppLoaderProvider>
							<AuthProvider>
								<Toaster />
								<AppRoutes />
							</AuthProvider>
						</AppLoaderProvider>
					</PersistQueryClientProvider>
				</TransportProvider>
			</BrowserRouter>
		</ThemeProvider>
	);
}
