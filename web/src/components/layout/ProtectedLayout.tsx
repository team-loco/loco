import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspace, WORKSPACE_STALE_MS } from "@/hooks/useWorkspace";
import { refetchUnlessMissing, resourceMissing, routeKnown } from "@/lib/not-found";
import { NotFound, NotFoundBoundary } from "@/pages/NotFound";
import { useAuth } from "@/auth/AuthProvider";
import { nextPathState, signInOnClickState } from "@/auth/next";
import { ClaimedOrgsBanner } from "@/components/ClaimedOrgsBanner";
import { ErrorCard } from "@/components/ErrorCard";
import { PageLoading } from "@/components/design/Page";
import { AppShell } from "@/components/shell/AppShell";
import { KeyboardShortcutsProvider } from "@/components/shell/KeyboardShortcuts";
import { CommandPaletteProvider } from "@/components/shell/CommandMenu";
import { AppLoading } from "@/context/AppLoader";
import { ContextProvider, ORG_STORAGE_KEY, pickActive, WORKSPACE_STORAGE_KEY } from "@/context/ContextProvider";
import { useStoredValue } from "@/lib/storage";
import { inSystem, systemAccess } from "@/lib/system";
import { getOrg, listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQuery } from "@connectrpc/connect-query";
import type { ReactNode } from "react";
import { Navigate, useLocation, useParams } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const { orgId: orgParam, workspaceId: workspaceParam } = useParams();
	const { pathname, search, hash } = useLocation();
	const { user, signedOut, signInHeld, isPending, error } = useAuth();

	const orgsQuery = useQuery(listUserOrgs, { userId: user?.id ?? "" }, { enabled: !!user });
	const orgs = orgsQuery.data?.orgs;
	const scopes = useMyScopes({ enabled: !!user });

	const [storedOrg] = useStoredValue(ORG_STORAGE_KEY, "");
	const activeOrgId = pickActive(orgs, orgParam, storedOrg);

	// Without a workspace in the URL, the active one is the remembered workspace if it is in the organization, else its newest.
	const picking =
		workspaceParam === undefined && activeOrgId !== null && !scopes.isLoading && scopes.orgMember(activeOrgId);
	const [stored] = useStoredValue(WORKSPACE_STORAGE_KEY, "");
	const newest = useQuery(listOrgWorkspaces, { orgId: activeOrgId ?? "", pageSize: 1 }, { enabled: picking });
	const remembered = useWorkspace(picking ? activeOrgId : null, stored === "" ? null : stored);
	const pickable = [...(newest.data?.workspaces ?? []), remembered.workspace].filter(
		(w): w is Workspace => w?.orgId === activeOrgId,
	);

	const rememberedInOrg = remembered.workspace?.orgId === activeOrgId;
	const lookupsLoading = newest.isLoading || remembered.isLoading;
	const awaitingPick = !rememberedInOrg && lookupsLoading;
	const orgsKnown = orgParam !== undefined || orgs !== undefined;
	const workspacesSettled = orgsKnown && !scopes.isLoading && !(picking && awaitingPick);
	const workspaceAccess =
		orgParam !== undefined && workspaceParam !== undefined ? scopes.workspaceLevel(orgParam, workspaceParam) : 0;
	const workspaceLookup = workspaceAccess >= 1;
	const urlWorkspace = useQuery(
		getWorkspace,
		{ workspaceId: workspaceParam ?? "" },
		{
			enabled: workspaceLookup,
			staleTime: WORKSPACE_STALE_MS,
			retryOnMount: false,
			refetchOnWindowFocus: refetchUnlessMissing(true),
		},
	);
	// A caller whose scopes cover every organization (system) can open an ID that is not in its list.
	const unlisted = orgs !== undefined && !orgs.some((o) => o.id === orgParam);
	const orgAccess = orgParam !== undefined && scopes.orgMember(orgParam);
	const orgLookup = unlisted && orgAccess;
	const urlOrg = useQuery(
		getOrg,
		{ orgId: orgParam ?? "" },
		{ enabled: orgLookup, refetchOnWindowFocus: refetchUnlessMissing(false) },
	);

	// An organization the caller reaches only through its scopes cannot be told from a missing one until the lookups answer.
	const orgPending = urlOrg.isPending;
	const workspacePending = workspaceLookup && urlWorkspace.isPending;
	const lookupPending = orgPending || workspacePending;
	// A System address depends on the scopes alone, never on the organization list.
	const systemRoute = inSystem(pathname);
	const systemPending = systemRoute && scopes.isLoading;
	const systemAbsent = systemRoute && !scopes.isLoading && systemAccess(scopes.scopes) === "none";
	const routeLoading = (orgLookup && lookupPending) || systemPending;
	const orgsAnswered = !scopes.isLoading && !orgsQuery.isPending;
	const routeDecided = orgsAnswered && !routeLoading;
	// not_found and invalid_argument mean the organization or workspace is absent. A denial counts as absent
	// only for the workspace lookup, which runs for a caller who can read the workspace.
	const lookupMissing = resourceMissing(urlOrg.error, false) || resourceMissing(urlWorkspace.error, true);
	const routeAbsent =
		orgParam !== undefined &&
		(lookupMissing ||
			!routeKnown(
				{ orgId: orgParam, workspaceId: workspaceParam, workspaceOrgId: urlWorkspace.data?.workspace?.orgId },
				scopes.scopes,
			));
	const routeNotFound = systemAbsent || (routeDecided && routeAbsent);

	if (signedOut) {
		return (
			<Navigate to="/login" replace state={signInHeld ? signInOnClickState : nextPathState(pathname + search + hash)} />
		);
	}

	if (isPending) {
		return <AppLoading />;
	}

	if (error) {
		return <ErrorCard error={error} fallbackMessage="Could not load your account" minHeight="min-h-screen" />;
	}

	return (
		<ContextProvider availableOrgs={orgs} pickableWorkspaces={pickable} workspacesSettled={workspacesSettled}>
			<KeyboardShortcutsProvider>
				<CommandPaletteProvider>
					{routeNotFound ? (
						<NotFound unknownWorkspace />
					) : (
						<NotFoundBoundary path={pathname}>
							<AppShell>{routeLoading ? <PageLoading /> : children}</AppShell>
						</NotFoundBoundary>
					)}
				</CommandPaletteProvider>
			</KeyboardShortcutsProvider>
			<ClaimedOrgsBanner />
		</ContextProvider>
	);
}
