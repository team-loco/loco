import { AuthProvider, QUERY_CACHE_KEY } from "@/auth/AuthProvider";
import { PageTitle } from "@/components/PageTitle";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { PageLoading } from "@/components/design/Page";
import { Toaster } from "@/components/design/Sonner";
import { ThemeProvider } from "@/lib/theme-provider";
import { Login } from "@/pages/Login";
import { AuthCallback } from "@/pages/auth/AuthCallback";
import { CliDevice } from "@/pages/cli/CliDevice";
import { CliLogin } from "@/pages/cli/CliLogin";
import { ResourceTitle } from "@/pages/resource/ResourceTitle";
import { Splash } from "@/pages/Splash";
import { createConnectQueryKey, TransportProvider, useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { createAsyncStoragePersister } from "@tanstack/query-async-storage-persister";
import { defaultShouldDehydrateQuery, QueryClient } from "@tanstack/react-query";
import { PersistQueryClientProvider, type AsyncStorage } from "@tanstack/react-query-persist-client";
import { lazy, Suspense } from "react";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { AppLoaderProvider, AppLoading } from "@/context/AppLoader";
import { NuqsAdapter } from "nuqs/adapters/react-router/v8";
import {
	createBrowserRouter,
	Navigate,
	Outlet,
	Route,
	RouterProvider,
	Routes,
	useLocation,
	useParams,
} from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironmentInUrl } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { notFound } from "@/lib/not-found";
import { accountSettingsPath, newResourcePath, PROFILE_PATH, settingsPath } from "@/lib/routes";
import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

import { pageImporters } from "@/lib/lazy-pages";

const Dashboard = lazy(async () => ({ default: (await pageImporters.Dashboard()).Dashboard }));
const Resources = lazy(async () => ({ default: (await pageImporters.Resources()).Resources }));
const Resource = lazy(async () => ({ default: (await pageImporters.Resource()).Resource }));
const Observability = lazy(async () => ({ default: (await pageImporters.Observability()).Observability }));
const Webhooks = lazy(async () => ({ default: (await pageImporters.Webhooks()).Webhooks }));
const Secrets = lazy(async () => ({ default: (await pageImporters.Secrets()).Secrets }));
const Settings = lazy(async () => ({ default: (await pageImporters.Settings()).Settings }));
const Team = lazy(async () => ({ default: (await pageImporters.Team()).Team }));
const Audit = lazy(async () => ({ default: (await pageImporters.Audit()).Audit }));
const Tokens = lazy(async () => ({ default: (await pageImporters.Tokens()).Tokens }));
const Organizations = lazy(async () => ({ default: (await pageImporters.Organizations()).Organizations }));
const PersonalSettings = lazy(async () => ({ default: (await pageImporters.Settings()).PersonalSettings }));
const DashboardRedirect = lazy(async () => ({ default: (await pageImporters.DashboardRedirect()).DashboardRedirect }));

import { transport } from "./auth/connect-transport";

// Every page mounts these; observers that mount while a page loads reuse the data fetched a moment earlier.
const SHARED_STALE_MS = 10_000;
const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			retry: false,
			gcTime: 1000 * 60 * 60 * 24,
		},
		mutations: {
			retry: false,
		},
	},
});

const sharedQueryKeys = [
	createConnectQueryKey({ schema: getConfig, cardinality: undefined }),
	createConnectQueryKey({ schema: getScopes, cardinality: undefined }),
	createConnectQueryKey({ schema: listEnvironments, cardinality: undefined }),
];
for (const queryKey of sharedQueryKeys) queryClient.setQueryDefaults(queryKey, { staleTime: SHARED_STALE_MS });

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
	key: QUERY_CACHE_KEY,
});

const persistOptions = {
	persister,
	buster: "dashboard-session",
	dehydrateOptions: {
		shouldDehydrateQuery: (query: Parameters<typeof defaultShouldDehydrateQuery>[0]) =>
			query.meta?.["persist"] !== false && defaultShouldDehydrateQuery(query),
	},
};

// Non-shallow updates keep React Router's location in step, and push matches its setSearchParams default.
const NUQS_DEFAULTS = { shallow: false, history: "push" } as const;

function NewResourceRedirect() {
	const { orgId = "", workspaceId = "" } = useParams();
	return <Navigate to={newResourcePath(orgId, workspaceId, useLocation().search)} replace />;
}

function OrgRedirect({ to }: { to: "team" | "settings" }) {
	const { hash } = useLocation();
	const { orgId } = useParams();
	const { activeOrgId, workspacesSettled } = useOrgWorkspace();
	const scopes = useMyScopes();
	const org = orgId ?? activeOrgId;
	const first = useQuery(
		listOrgWorkspaces,
		{ orgId: org ?? "", pageSize: 1 },
		{ enabled: to === "settings" && org !== null && !scopes.isLoading && scopes.orgMember(org) },
	);
	if (!workspacesSettled || scopes.isLoading || first.isLoading) return <PageLoading />;
	if (org === null) return <Navigate to="/organizations" replace />;
	if (to === "team") return <Navigate to={`/org/${org}/team`} replace />;
	const ws = first.data?.workspaces[0]?.id;
	if (ws === undefined) return <Navigate to="/organizations" replace />;
	return <Navigate to={`${settingsPath(org, ws, "org")}${hash}`} replace />;
}

function WorkspaceRoutes() {
	useEnvironmentInUrl();
	return <Outlet />;
}

function NoRoute(): never {
	notFound();
}

function ProfileRedirect() {
	const { activeOrgId, activeWorkspaceId, workspacesSettled } = useOrgWorkspace();
	if (!workspacesSettled) return <PageLoading />;
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
					<Route
						path="/org/:orgId/audit"
						element={
							<>
								<PageTitle title="Audit log" />
								<Audit />
							</>
						}
					/>
					<Route path="/org/:orgId/settings" element={<OrgRedirect to="settings" />} />

					<Route path="/org/:orgId/wks/:workspaceId" element={<WorkspaceRoutes />}>
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
						<Route
							path="secrets"
							element={
								<>
									<PageTitle title="Secrets" />
									<Secrets />
								</>
							}
						/>
						<Route
							path="webhooks"
							element={
								<>
									<PageTitle title="Webhooks" />
									<Webhooks />
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

function AppProviders() {
	return (
		<NuqsAdapter defaultOptions={NUQS_DEFAULTS}>
			<TransportProvider transport={transport}>
				<PersistQueryClientProvider
					client={queryClient}
					persistOptions={persistOptions}
					onSuccess={() => {
						void queryClient.invalidateQueries();
					}}
				>
					<AppLoaderProvider>
						<AuthProvider>
							<ThemeProvider>
								<Toaster />
								<ErrorBoundary>
									<AppRoutes />
								</ErrorBoundary>
							</ThemeProvider>
						</AuthProvider>
					</AppLoaderProvider>
				</PersistQueryClientProvider>
			</TransportProvider>
		</NuqsAdapter>
	);
}

const router = createBrowserRouter([{ path: "*", Component: AppProviders }]);

export default function App() {
	return <RouterProvider router={router} />;
}
