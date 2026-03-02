import Loader from "@/assets/loader.svg?react";
import { useAuth } from "@/auth/AuthProvider";
import { EmptyState } from "@/components/EmptyState";
import { ErrorCard } from "@/components/ErrorCard";
import { BentoDashboard } from "@/components/dashboard/BentoDashboard";
import { WorkspaceDashboardMetrics } from "@/components/dashboard/WorkspaceDashboardMetrics";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useHeader } from "@/context/HeaderContext";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { listWorkspaceResources } from "@/gen/loco/resource/v1";
import { listOrgWorkspaces } from "@/gen/loco/workspace/v1";
import { subscribeToEvents } from "@/lib/events";
import { useQuery } from "@connectrpc/connect-query";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

export function Home() {
	const navigate = useNavigate();
	const { logout, user } = useAuth();
	const { setHeader } = useHeader();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");
	const selectedWorkspaceId = workspaceFromUrl ?? null;
	const [searchTerm] = useState("");

	// Fetch all organizations
	const {
		data: orgsQueryRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(listUserOrgs, user ? { userId: user.id } : undefined, {
		enabled: !!user,
	});
	const orgs = useMemo(() => orgsQueryRes?.orgs ?? [], [orgsQueryRes]);

	// Use org/workspace from context
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const currentOrgId = activeOrgId ?? (orgs.length > 0 ? orgs[0].id : null);

	// Fetch workspaces for selected org
	const { data: listWorkspacesRes } = useQuery(
		listOrgWorkspaces,
		currentOrgId ? { orgId: currentOrgId } : undefined,
		{ enabled: !!currentOrgId },
	);
	const workspaces = useMemo(
		() => listWorkspacesRes?.workspaces ?? [],
		[listWorkspacesRes],
	);
	const currentWorkspaceId =
		activeWorkspaceId ??
		selectedWorkspaceId ??
		(workspaces.length > 0 ? workspaces[0].id : null);

	// Fetch resources in parallel after we have workspace ID
	const {
		data: listResourcesRes,
		isLoading: resourcesLoading,
		error: resourcesError,
		refetch: refetchResources,
	} = useQuery(
		listWorkspaceResources,
		currentWorkspaceId ? { workspaceId: currentWorkspaceId } : undefined,
		{ enabled: !!currentWorkspaceId },
	);

	const allResources = useMemo(
		() => listResourcesRes?.resources ?? [],
		[listResourcesRes?.resources],
	);

	// Filter resources by search term
	const filteredResources = useMemo(() => {
		if (!searchTerm.trim()) {
			return allResources;
		}
		return allResources.filter((resource) =>
			resource.name.toLowerCase().includes(searchTerm.toLowerCase()),
		);
	}, [allResources, searchTerm]);

	// Set header content
	useEffect(() => {
		const currentWorkspace = workspaces.find(
			(ws) => ws.id === currentWorkspaceId,
		);
		const workspaceName = currentWorkspace?.name ?? "Workspace";

		setHeader(
			<h2 className="text-2xl font-mono text-foreground">
				workspaces::{workspaceName}
			</h2>,
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
				void refetchResources();
			}
		});

		return unsubscribe;
	}, [refetchResources]);

	const isLoading = orgsLoading || resourcesLoading;
	const error = orgsError ?? resourcesError;

	// Handle auth failures by redirecting to login
	useEffect(() => {
		if (orgsError) {
			void logout();
			void navigate("/login", { replace: true });
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
		return <ErrorCard error={error} fallbackMessage="Failed to load resources" />;
	}

	return (
		<div className="space-y-4">
			{/* Workspace Dashboard Metrics - only show when workspace is selected */}
			{currentWorkspaceId && (
				<WorkspaceDashboardMetrics
					workspaceId={currentWorkspaceId}
					workspaceName={
						workspaces.find((ws) => ws.id === currentWorkspaceId)?.name ?? ""
					}
				/>
			)}

			{/* Applications and Deployments */}
			{true ? (
				<div className="mt-8">
					<BentoDashboard
						resources={filteredResources.length > 0 ? filteredResources : []}
						workspaceId={currentWorkspaceId ?? undefined}
					/>
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
					action={
						currentOrgId && currentWorkspaceId
							? {
									label: "Create Your First Resource",
									onClick: () => {
										void navigate(
											`/org/${currentOrgId.toString()}/wks/${currentWorkspaceId.toString()}/create-resource`,
										);
									},
								}
							: undefined
					}
				/>
			)}
		</div>
	);
}
