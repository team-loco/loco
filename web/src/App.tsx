import { AuthProvider } from "@/auth/AuthProvider";
import { PageTitle } from "@/components/PageTitle";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { Toaster } from "@/components/design/Sonner";
import { ThemeProvider } from "@/lib/theme-provider";
import { Login } from "@/pages/Login";
import { AuthCallback } from "@/pages/auth/AuthCallback";
import { CliDevice } from "@/pages/cli/CliDevice";
import { CliLogin } from "@/pages/cli/CliLogin";
import { ResourceTitle } from "@/pages/resource/ResourceTitle";
import { Splash } from "@/pages/Splash";
import { TransportProvider } from "@connectrpc/connect-query";
import { createAsyncStoragePersister } from "@tanstack/query-async-storage-persister";
import { QueryClient } from "@tanstack/react-query";
import { PersistQueryClientProvider, type AsyncStorage } from "@tanstack/react-query-persist-client";
import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { AppLoaderProvider, AppLoading } from "@/context/AppLoader";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useParams } from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { notFound } from "@/lib/not-found";
import { accountSettingsPath, newResourcePath, PROFILE_PATH, settingsPath } from "@/lib/routes";
import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

import { pageImporters } from "@/lib/lazy-pages";

const Dashboard = lazy(async () => ({ default: (await pageImporters.Dashboard()).Dashboard }));
const Resources = lazy(async () => ({ default: (await pageImporters.Resources()).Resources }));
const Resource = lazy(async () => ({ default: (await pageImporters.Resource()).Resource }));
const Observability = lazy(async () => ({ default: (await pageImporters.Observability()).Observability }));
const Settings = lazy(async () => ({ default: (await pageImporters.Settings()).Settings }));
const Team = lazy(async () => ({ default: (await pageImporters.Team()).Team }));
const Tokens = lazy(async () => ({ default: (await pageImporters.Tokens()).Tokens }));
const Organizations = lazy(async () => ({ default: (await pageImporters.Organizations()).Organizations }));
const PersonalSettings = lazy(async () => ({ default: (await pageImporters.Settings()).PersonalSettings }));
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

function NewResourceRedirect() {
	const { orgId = "", workspaceId = "" } = useParams();
	const params = Object.fromEntries(new URLSearchParams(useLocation().search));
	return <Navigate to={newResourcePath(orgId, workspaceId, params)} replace />;
}

function OrgRedirect({ to }: { to: "team" | "settings" }) {
	const { orgId } = useParams();
	const { activeOrgId, workspaces, workspacesSettled } = useOrgWorkspace();
	const org = orgId ?? activeOrgId;
	if (org === null) return <Navigate to="/organizations" replace />;
	if (to === "team") return <Navigate to={`/org/${org}/team`} replace />;
	if (!workspacesSettled) return <AppLoading />;
	const ws = workspaces.find((w) => w.orgId === org)?.id;
	if (ws === undefined) return <Navigate to="/organizations" replace />;
	return <Navigate to={settingsPath(org, ws, "org")} replace />;
}

function NoRoute(): never {
	notFound();
}

function ProfileRedirect() {
	const { activeOrgId, activeWorkspaceId, workspacesSettled } = useOrgWorkspace();
	if (!workspacesSettled) return <AppLoading />;
	const to = accountSettingsPath(activeOrgId, activeWorkspaceId);
	if (to !== PROFILE_PATH) return <Navigate to={to} replace />;
	return (
		<>
			<PageTitle title="Settings" />
			<PersonalSettings />
		</>
	);
}

function AppRoutes() {
	return (
		<Suspense fallback={<AppLoading />}>
			<Routes>
				{/* Public routes */}
				<Route path="/" element={<Splash />} />
				<Route
					path="/login"
					element={
						<>
							<PageTitle title="Sign in" />
							<Login />
						</>
					}
				/>
				<Route path="/auth/callback" element={<AuthCallback />} />
				<Route
					path="/cli/login"
					element={
						<>
							<PageTitle title="Sign in to the CLI" />
							<CliLogin />
						</>
					}
				/>
				<Route
					path="/cli/device"
					element={
						<>
							<PageTitle title="Sign in a device" />
							<CliDevice />
						</>
					}
				/>
				<Route path="/onboarding" element={<Navigate to="/dashboard" replace />} />

				<Route element={<ProtectedRoute />}>
					<Route path="/dashboard" element={<DashboardRedirect />} />
					<Route
						path="/organizations"
						element={
							<>
								<PageTitle title="Organizations" />
								<Organizations />
							</>
						}
					/>
					<Route path="/profile" element={<ProfileRedirect />} />
					<Route
						path="/tokens"
						element={
							<>
								<PageTitle title="Tokens" />
								<Tokens />
							</>
						}
					/>
					<Route path="/team" element={<OrgRedirect to="team" />} />
					<Route
						path="/org/:orgId/team"
						element={
							<>
								<PageTitle title="Team" />
								<Team />
							</>
						}
					/>
					<Route path="/org/:orgId/settings" element={<OrgRedirect to="settings" />} />

					<Route path="/org/:orgId/wks/:workspaceId">
						<Route
							path=""
							element={
								<>
									<PageTitle title="Dashboard" />
									<Dashboard />
								</>
							}
						/>
						<Route path="dashboard" element={<Navigate to=".." relative="path" replace />} />
						<Route
							path="resources"
							element={
								<>
									<PageTitle title="Resources" />
									<Resources />
								</>
							}
						/>
						<Route path="new" element={<NewResourceRedirect />} />
						<Route
							path="resource/:resourceId"
							element={
								<>
									<ResourceTitle />
									<Resource />
								</>
							}
						/>
						<Route path="observability" element={<Observability />} />
						<Route path="events" element={<Navigate to="../observability?view=events" relative="path" replace />} />
						<Route
							path="settings"
							element={
								<>
									<PageTitle title="Settings" />
									<Settings />
								</>
							}
						/>
						<Route path="*" element={<NoRoute />} />
					</Route>
					<Route path="*" element={<NoRoute />} />
				</Route>
			</Routes>
		</Suspense>
	);
}

export default function App() {
	return (
		<BrowserRouter>
			<TransportProvider transport={transport}>
				<PersistQueryClientProvider client={queryClient} persistOptions={{ persister }}>
					<AppLoaderProvider>
						<Suspense fallback={<AppLoading />}>
							<AuthProvider>
								<ThemeProvider>
									<Toaster />
									<ErrorBoundary>
										<AppRoutes />
									</ErrorBoundary>
								</ThemeProvider>
							</AuthProvider>
						</Suspense>
					</AppLoaderProvider>
				</PersistQueryClientProvider>
			</TransportProvider>
		</BrowserRouter>
	);
}
