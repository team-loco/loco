import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { AppShell } from "@/components/shell/AppShell";
import { AppLoading } from "@/context/AppLoader";
import { ContextProvider, ORG_STORAGE_KEY, pickActive } from "@/context/ContextProvider";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import type { ReactNode } from "react";
import { Navigate, useParams } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const { orgId: orgParam } = useParams();
	const { user, signedOut } = useAuth();
	const { isPending, error } = useQuery(whoAmI, {}, { enabled: !signedOut });

	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? "" },
		{ enabled: !!user },
	);
	const orgs = orgsRes?.orgs ?? [];

	const activeOrgId = pickActive(orgs, orgParam, ORG_STORAGE_KEY);

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		activeOrgId ? { orgId: activeOrgId } : undefined,
		{ enabled: !!activeOrgId },
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	if (signedOut || unauthenticated) {
		return <Navigate to="/login" replace />;
	}

	if (isPending) {
		return <AppLoading />;
	}

	if (error) {
		return <ErrorCard error={error} fallbackMessage="Could not load your account" minHeight="min-h-screen" />;
	}

	return (
		<ContextProvider availableOrgs={orgs} availableWorkspaces={workspaces}>
			<AppShell>{children}</AppShell>
		</ContextProvider>
	);
}
