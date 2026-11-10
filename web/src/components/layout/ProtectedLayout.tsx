import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { useMyScopes } from "@/hooks/useMyScopes";
import { workspaceKnown } from "@/lib/not-found";
import { NotFound, NotFoundBoundary } from "@/pages/NotFound";
import { useAuth } from "@/auth/AuthProvider";
import { nextPathState } from "@/auth/next";
import { ClaimedOrgsBanner } from "@/components/ClaimedOrgsBanner";
import { ErrorCard } from "@/components/ErrorCard";
import { AppShell } from "@/components/shell/AppShell";
import { CommandPaletteProvider } from "@/components/shell/CommandMenu";
import { AppLoading } from "@/context/AppLoader";
import { ContextProvider, ORG_STORAGE_KEY, pickActive } from "@/context/ContextProvider";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Navigate, useLocation, useParams } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const { orgId: orgParam, workspaceId: workspaceParam } = useParams();
	const { pathname, search } = useLocation();
	const transport = useTransport();
	const { user, signedOut } = useAuth();
	const { isPending, error } = useQuery(whoAmI, {}, { enabled: !signedOut });

	const { data: orgsRes } = useQuery(listUserOrgs, { userId: user?.id ?? "" }, { enabled: !!user });
	const orgs = orgsRes?.orgs;
	const scopes = useMyScopes({ enabled: !!user });

	const activeOrgId = pickActive(orgs, orgParam, ORG_STORAGE_KEY);

	const { data: workspacesRes, isLoading: workspacesLoading } = useTanstackQuery({
		...orgWorkspacesOptions(transport, activeOrgId ?? ""),
		enabled: !!activeOrgId && !scopes.isLoading && scopes.orgMember(activeOrgId),
	});
	const workspaces = workspacesRes?.workspaces ?? [];

	const workspacesSettled = (orgParam !== undefined || orgs !== undefined) && !scopes.isLoading && !workspacesLoading;
	const workspaceNotFound =
		workspacesSettled &&
		workspaceParam !== undefined &&
		!workspaceKnown(activeOrgId ?? "", workspaceParam, workspaces, scopes.scopes);

	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	if (signedOut || unauthenticated) {
		return <Navigate to="/login" replace state={nextPathState(pathname + search)} />;
	}

	if (isPending) {
		return <AppLoading />;
	}

	if (error) {
		return <ErrorCard error={error} fallbackMessage="Could not load your account" minHeight="min-h-screen" />;
	}

	return (
		<ContextProvider availableOrgs={orgs} availableWorkspaces={workspaces} workspacesSettled={workspacesSettled}>
			<CommandPaletteProvider>
				{workspaceNotFound ? (
					<NotFound unknownWorkspace />
				) : (
					<NotFoundBoundary path={pathname}>
						<AppShell>{children}</AppShell>
					</NotFoundBoundary>
				)}
			</CommandPaletteProvider>
			<ClaimedOrgsBanner />
		</ContextProvider>
	);
}
