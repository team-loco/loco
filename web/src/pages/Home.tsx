import { useMemo, useState, useEffect } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AppCard } from "@/components/AppCard";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { OrgFilter } from "@/components/dashboard/OrgFilter";
import { AppSearch } from "@/components/dashboard/AppSearch";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { listApps } from "@/gen/app/v1";
import { useNavigate } from "react-router";
import { subscribeToEvents } from "@/lib/events";

export function Home() {
	const navigate = useNavigate();
	const [selectedOrgId, setSelectedOrgId] = useState<string | null>(null);
	const [searchTerm, setSearchTerm] = useState("");

	// Fetch all organizations
	const {
		data: getCurrentUserOrgsRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(getCurrentUserOrgs, {});
	const orgs = getCurrentUserOrgsRes?.orgs ?? [];

	// Set default org on load
	useMemo(() => {
		if (selectedOrgId === null && orgs.length > 0) {
			setSelectedOrgId(orgs[0].id);
		}
	}, [orgs, selectedOrgId]);

	const currentOrgId = selectedOrgId || (orgs.length > 0 ? orgs[0].id : null);

	// Fetch workspaces for selected org
	const { data: listWorkspacesRes, isLoading: workspacesLoading } = useQuery(
		listWorkspaces,
		currentOrgId ? { orgId: currentOrgId } : undefined,
		{ enabled: !!currentOrgId }
	);
	const workspaces = listWorkspacesRes?.workspaces ?? [];
	const currentWorkspaceId = workspaces.length > 0 ? workspaces[0].id : null;

	// Fetch all apps for selected workspace
	const {
		data: listAppsRes,
		isLoading: appsLoading,
		error: appsError,
		refetch: refetchApps,
	} = useQuery(
		listApps,
		currentWorkspaceId ? { workspaceId: currentWorkspaceId } : undefined,
		{ enabled: !!currentWorkspaceId }
	);
	const allApps = listAppsRes?.apps ?? [];

	// Filter apps by search term
	const filteredApps = useMemo(() => {
		if (!searchTerm.trim()) {
			return allApps;
		}
		return allApps.filter((app) =>
			app.name.toLowerCase().includes(searchTerm.toLowerCase())
		);
	}, [allApps, searchTerm]);

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
		<div className="space-y-6">
			{/* Header */}
			<div className="space-y-1">
				<h2 className="text-3xl font-heading text-foreground">Dashboard</h2>
				<p className="text-sm text-foreground opacity-70">
					Manage your applications and deployments
				</p>
			</div>

			{/* Controls: Org Filter, Search, Create Button */}
			<div className="flex flex-col sm:flex-row gap-4 items-start sm:items-center">
				<OrgFilter selectedOrgId={currentOrgId} onOrgChange={setSelectedOrgId} />
				<AppSearch searchTerm={searchTerm} onSearchChange={setSearchTerm} />
				<Button
					onClick={() => navigate("/create-app")}
					className="w-full sm:w-auto"
				>
					+ Create App
				</Button>
			</div>

			{/* Apps Grid */}
			<div className="space-y-4">
				<div className="flex items-center justify-between">
					<h3 className="text-2xl font-heading">
						{searchTerm ? "Search Results" : "Your Applications"}
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
