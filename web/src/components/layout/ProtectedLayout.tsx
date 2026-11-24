import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspace, WORKSPACE_STALE_MS } from "@/hooks/useWorkspace";
import { resourceMissing, routeKnown } from "@/lib/not-found";
import { NotFound, NotFoundBoundary } from "@/pages/NotFound";
import { useAuth } from "@/auth/AuthProvider";
import { nextPathState } from "@/auth/next";
import { ClaimedOrgsBanner } from "@/components/ClaimedOrgsBanner";
import { ErrorCard } from "@/components/ErrorCard";
import { AppShell } from "@/components/shell/AppShell";
import { KeyboardShortcutsProvider } from "@/components/shell/KeyboardShortcuts";
import { CommandPaletteProvider } from "@/components/shell/CommandMenu";
import { AppLoading } from "@/context/AppLoader";
import { ContextProvider, ORG_STORAGE_KEY, pickActive, WORKSPACE_STORAGE_KEY } from "@/context/ContextProvider";
import { useStoredValue } from "@/lib/storage";
import { getOrg, listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQuery } from "@connectrpc/connect-query";
import { useState, type ReactNode } from "react";
import { Navigate, useLocation, useParams } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

/** An answer that is not known yet is `loading`; nothing is drawn from it until it is `found` or `notFound`. */
type RouteState = "loading" | "found" | "notFound";

function undecided(query: { data: unknown; error: unknown }, enabled: boolean): boolean {
	return enabled && query.data === undefined && query.error === null;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const { orgId: orgParam, workspaceId: workspaceParam } = useParams();
	const { pathname, search, hash } = useLocation();
	const { user, signedOut, isPending, error } = useAuth();

	const orgsQuery = useQuery(listUserOrgs, { userId: user?.id ?? "" }, { enabled: !!user });
	const orgs = orgsQuery.data?.orgs;
	const scopes = useMyScopes({ enabled: !!user });

	const [storedOrg] = useStoredValue(ORG_STORAGE_KEY, "");
	const activeOrgId = pickActive(orgs, orgParam, storedOrg === "" ? null : storedOrg);

	// Without a workspace in the URL, the active one is the remembered workspace if it is in the organization, else its newest.
	const picking =
		workspaceParam === undefined && activeOrgId !== null && !scopes.isLoading && scopes.orgMember(activeOrgId);
	const [stored] = useStoredValue(WORKSPACE_STORAGE_KEY, "");
	const newest = useQuery(listOrgWorkspaces, { orgId: activeOrgId ?? "", pageSize: 1 }, { enabled: picking });
	const remembered = useWorkspace(picking ? activeOrgId : null, stored === "" ? null : stored);
	const pickable = [...(newest.data?.workspaces ?? []), remembered.workspace].filter(
		(w): w is Workspace => w?.orgId === activeOrgId,
	);

	// A remembered workspace of the organization is the pick whatever the newest list says, so the list is not waited for.
	const rememberedHere = remembered.workspace?.orgId === activeOrgId;
	const workspacesSettled =
		(orgParam !== undefined || orgs !== undefined) &&
		!scopes.isLoading &&
		!(picking && !rememberedHere && (newest.isLoading || remembered.isLoading));

	const orgsAnswered = !scopes.isLoading && !orgsQuery.isLoading;
	const workspaceLookup =
		orgsAnswered &&
		orgParam !== undefined &&
		workspaceParam !== undefined &&
		scopes.workspaceLevel(orgParam, workspaceParam) >= 1;
	const urlWorkspace = useQuery(
		getWorkspace,
		{ workspaceId: workspaceParam ?? "" },
		{ enabled: workspaceLookup, staleTime: WORKSPACE_STALE_MS, retryOnMount: false },
	);
	// A caller whose scopes cover every organization (system) can open an ID that is not in its list.
	const orgLookup =
		orgsAnswered &&
		orgParam !== undefined &&
		orgs !== undefined &&
		!orgs.some((o) => o.id === orgParam) &&
		scopes.orgMember(orgParam);
	const urlOrg = useQuery(getOrg, { orgId: orgParam ?? "" }, { enabled: orgLookup });

	let routeState: RouteState = "found";
	if (orgParam !== undefined) {
		if (!orgsAnswered || undecided(urlOrg, orgLookup) || undecided(urlWorkspace, workspaceLookup)) {
			routeState = "loading";
		} else if (
			// not_found and invalid_argument mean the organization or workspace is absent. A denial counts as absent
			// only for the workspace lookup, which runs for a caller who can read the workspace.
			resourceMissing(urlOrg.error, false) ||
			resourceMissing(urlWorkspace.error, true) ||
			!routeKnown(
				{ orgId: orgParam, workspaceId: workspaceParam, workspaceOrgId: urlWorkspace.data?.workspace?.orgId },
				scopes.scopes,
			)
		) {
			routeState = "notFound";
		}
	}
	// A shell already on screen stays while the next route is looked up; only the first draw waits for the answer.
	const [shellShown, setShellShown] = useState(false);
	if (routeState === "found" && !shellShown) setShellShown(true);

	if (signedOut) {
		return <Navigate to="/login" replace state={nextPathState(pathname + search + hash)} />;
	}

	if (isPending || (routeState === "loading" && !shellShown)) {
		return <AppLoading />;
	}

	if (error) {
		return <ErrorCard error={error} fallbackMessage="Could not load your account" minHeight="min-h-screen" />;
	}

	return (
		<ContextProvider availableOrgs={orgs} pickableWorkspaces={pickable} workspacesSettled={workspacesSettled}>
			<KeyboardShortcutsProvider>
				<CommandPaletteProvider>
					{routeState === "notFound" ? (
						<NotFound unknownWorkspace />
					) : (
						<NotFoundBoundary path={pathname}>
							<AppShell>{children}</AppShell>
						</NotFoundBoundary>
					)}
				</CommandPaletteProvider>
			</KeyboardShortcutsProvider>
			<ClaimedOrgsBanner />
		</ContextProvider>
	);
}
