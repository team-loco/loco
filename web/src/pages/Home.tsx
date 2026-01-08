import { useAuth } from "@/auth/AuthProvider";
import { AppCard } from "@/components/AppCard";
import { EmptyState } from "@/components/EmptyState";
import { WorkspaceDashboardMetrics } from "@/components/dashboard/WorkspaceDashboardMetrics";
import { Card, CardContent } from "@/components/ui/card";
import { useHeader } from "@/context/HeaderContext";
import { listWorkspaceResources } from "@/gen/resource/v1";
import { listUserOrgs } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { subscribeToEvents } from "@/lib/events";
import { getErrorMessage } from "@/lib/error-handler";
import { useQuery } from "@connectrpc/connect-query";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import Loader from "@/assets/loader.svg?react";
import { useOrgContext } from "@/hooks/useOrgContext";

export function Home() {
	const navigate = useNavigate();
	const { logout, user } = useAuth();
	const { setHeader } = useHeader();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const selectedWorkspaceId = workspaceFromUrl
		? BigInt(workspaceFromUrl)
		: null;
	const [searchTerm] = useState("");

	// Fetch all organizations
	const {
		data: orgsQueryRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(listUserOrgs, user ? { userId: BigInt(user.id) } : undefined, {
		enabled: !!user,
	});
	const orgs = useMemo(() => orgsQueryRes?.orgs ?? [], [orgsQueryRes]);

	// Use org context from URL (managed by useOrgContext hook)
	const { activeOrgId } = useOrgContext(orgs.map((o) => o.id));
	const currentOrgId = activeOrgId ?? (orgs.length > 0 ? orgs[0].id : null);

	// Fetch workspaces for selected org
	const { data: listWorkspacesRes } = useQuery(
		listOrgWorkspaces,
		currentOrgId ? { orgId: currentOrgId } : undefined,
		{ enabled: !!currentOrgId }
	);
	const workspaces = useMemo(
		() => listWorkspacesRes?.workspaces ?? [],
		[listWorkspacesRes]
	);
	const currentWorkspaceId =
		selectedWorkspaceId || (workspaces.length > 0 ? workspaces[0].id : null);

	// Fetch resources in parallel after we have workspace ID
	const {
		data: listResourcesRes,
		isLoading: resourcesLoading,
		error: resourcesError,
		refetch: refetchResources,
	} = useQuery(
		listWorkspaceResources,
		{ workspaceId: currentWorkspaceId ?? 0n },
		{ enabled: !!currentWorkspaceId }
	);

	const allResources = useMemo(
		() => listResourcesRes?.resources ?? [],
		[listResourcesRes?.resources]
	);

	// Filter resources by search term
	const filteredResources = useMemo(() => {
		if (!searchTerm.trim()) {
			return allResources;
		}
		return allResources.filter((resource) =>
			resource.name.toLowerCase().includes(searchTerm.toLowerCase())
		);
	}, [allResources, searchTerm]);

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

	// Subscribe to real-time resource status updates
	useEffect(() => {
		const unsubscribe = subscribeToEvents("workspace", (event) => {
			// Refetch resources when deployment status changes
			if (
				event.type === "deployment_started" ||
				event.type === "deployment_completed" ||
				event.type === "deployment_failed"
			) {
				refetchResources();
			}
		});

		return unsubscribe;
	}, [refetchResources]);

	const isLoading = orgsLoading || resourcesLoading;
	const error = orgsError || resourcesError;

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
					<div className="inline-flex gap-2 items-center flex-col">
						<Loader className="w-8 h-8" />
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
							{getErrorMessage(error, "Failed to load resources")}
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<div className="space-y-4">
			{/* Workspace Dashboard Metrics - only show when workspace is selected */}
			{currentWorkspaceId && (
				<WorkspaceDashboardMetrics
					workspaceId={currentWorkspaceId}
					workspaceName={
						workspaces.find((ws) => ws.id === currentWorkspaceId)?.name || ""
					}
				/>
			)}

			{/* Resources Grid */}
			<div className="space-y-4">
				<div className="flex items-center justify-between">
					<h3 className="text-2xl font-heading">
						{searchTerm ? "Search Results" : "Resources"}
					</h3>
					{allResources.length > 0 && (
						<p className="text-sm text-foreground opacity-60">
							{filteredResources.length} of {allResources.length}
						</p>
					)}
				</div>

				{filteredResources.length > 0 ? (
					<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
						{filteredResources.map((resource) => (
							<AppCard
								key={resource.id}
								resource={resource}
								onResourceDeleted={() => refetchResources()}
								workspaceId={currentWorkspaceId || undefined}
							/>
						))}
					</div>
				) : allResources.length > 0 ? (
					<EmptyState
						title="No Results"
						description={`No resources match "${searchTerm}"`}
					/>
				) : (
					<EmptyState
						title="No Resources Yet"
						description="Create your first resource to get started with Loco"
						action={{
							label: "Create Your First Resource",
							onClick: () => navigate("/create-resource"),
						}}
					/>
				)}
			</div>
		</div>
	);
}
