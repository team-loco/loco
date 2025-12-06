import { useAuth } from "@/auth/AuthProvider";
import { AppCard } from "@/components/AppCard";
import { EmptyState } from "@/components/EmptyState";
import { WorkspaceDashboardMetrics } from "@/components/dashboard/WorkspaceDashboardMetrics";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useHeader } from "@/context/HeaderContext";
import { listApps } from "@/gen/app/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { subscribeToEvents } from "@/lib/events";
import { useQuery } from "@connectrpc/connect-query";
import { Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

export function Home() {
	const navigate = useNavigate();
	const { logout } = useAuth();
	const { setHeader } = useHeader();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const [selectedOrgId] = useState<bigint | null>(null);
	const selectedWorkspaceId = workspaceFromUrl
		? BigInt(workspaceFromUrl)
		: null;
	const [searchTerm] = useState("");

	// Fetch all organizations
	const {
		data: getCurrentUserOrgsRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(getCurrentUserOrgs, {});
	const orgs = useMemo(
		() => getCurrentUserOrgsRes?.orgs ?? [],
		[getCurrentUserOrgsRes]
	);

	const currentOrgId = selectedOrgId || (orgs.length > 0 ? orgs[0].id : null);

	// Fetch workspaces for selected org
	const { data: listWorkspacesRes, isLoading: workspacesLoading } = useQuery(
		listWorkspaces,
		currentOrgId ? { orgId: currentOrgId } : undefined,
		{ enabled: !!currentOrgId }
	);
	const workspaces = useMemo(
		() => listWorkspacesRes?.workspaces ?? [],
		[listWorkspacesRes]
	);
	const currentWorkspaceId =
		selectedWorkspaceId || (workspaces.length > 0 ? workspaces[0].id : null);

	// Fetch all apps for selected workspace (only if workspace is selected)
	const {
		data: listAppsRes,
		isLoading: appsLoading,
		error: appsError,
		refetch: refetchApps,
	} = useQuery(
		listApps,
		{ workspaceId: currentWorkspaceId ?? 0n },
		{ enabled: !!currentWorkspaceId }
	);

	const allApps = useMemo(() => listAppsRes?.apps ?? [], [listAppsRes?.apps]);

	// Filter apps by search term
	const filteredApps = useMemo(() => {
		if (!searchTerm.trim()) {
			return allApps;
		}
		return allApps.filter((app) =>
			app.name.toLowerCase().includes(searchTerm.toLowerCase())
		);
	}, [allApps, searchTerm]);

	// Set header content
	useEffect(() => {
		const currentWorkspace = workspaces.find(
			(ws) => ws.id === currentWorkspaceId
		);
		const workspaceName = currentWorkspace?.name || "Workspace";

		setHeader(
			<h2 className="text-2xl font-mono text-foreground">
				workspaces::{workspaceName}
			</h2>
		);
	}, [setHeader, workspaces, currentWorkspaceId]);

	// Subscribe to real-time app status updates
	useEffect(() => {
		const unsubscribe = subscribeToEvents("workspace", (event) => {
			// Refetch apps when deployment status changes
			if (
				event.type === "deployment_started" ||
				event.type === "deployment_completed" ||
				event.type === "deployment_failed"
			) {
				refetchApps();
			}
		});

		return unsubscribe;
	}, [refetchApps]);

	const isLoading = orgsLoading || workspacesLoading || appsLoading;
	const error = orgsError || appsError;

	// Handle auth failures by redirecting to login
	useEffect(() => {
		if (orgsError) {
			logout();
			navigate("/login", { replace: true });
		}
	}, [orgsError, logout, navigate]);

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center">
					<div className="inline-flex gap-2 items-center">
						<div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
						<p className="text-foreground font-base">Loading...</p>
					</div>
				</div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-4">
							Error Loading Data
						</p>
						<p className="text-sm text-foreground opacity-70 mb-4">
							{error instanceof Error ? error.message : "Unknown error"}
						</p>
						<p className="text-xs text-foreground opacity-50">
							Make sure the backend is running on http://localhost:8000
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<div className="space-y-4">
			{/* Create Button */}
			{allApps.length > 0 && (
				<div className="flex justify-end">
					<Button
						onClick={() => navigate("/create-app")}
						size="sm"
						className="gap-2"
					>
						<Plus className="h-4 w-4" />
						Create App
					</Button>
				</div>
			)}

			{/* Workspace Dashboard Metrics - only show when workspace is selected */}
			{currentWorkspaceId && (
				<WorkspaceDashboardMetrics
					workspaceId={currentWorkspaceId}
					workspaceName={
						workspaces.find((ws) => ws.id === currentWorkspaceId)?.name || ""
					}
				/>
			)}

			{/* Apps Grid */}
			<div className="space-y-4">
				<div className="flex items-center justify-between">
					<h3 className="text-2xl font-heading">
						{searchTerm ? "Search Results" : "Applications"}
					</h3>
					{allApps.length > 0 && (
						<p className="text-sm text-foreground opacity-60">
							{filteredApps.length} of {allApps.length}
						</p>
					)}
				</div>

				{filteredApps.length > 0 ? (
					<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
						{filteredApps.map((app) => (
							<AppCard
								key={app.id}
								app={app}
								onAppDeleted={() => refetchApps()}
								workspaceId={currentWorkspaceId || undefined}
							/>
						))}
					</div>
				) : allApps.length > 0 ? (
					<EmptyState
						title="No Results"
						description={`No apps match "${searchTerm}"`}
					/>
				) : (
					<EmptyState
						title="No Applications Yet"
						description="Create your first application to get started with Loco"
						action={{
							label: "Create Your First App",
							onClick: () => navigate("/create-app"),
						}}
					/>
				)}
			</div>
		</div>
	);
}
