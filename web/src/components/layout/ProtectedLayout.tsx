import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { RouteFallback } from "@/components/RouteFallback";
import { AppShell } from "@/components/shell/AppShell";
import { ContextProvider } from "@/context/ContextProvider";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import type { ReactNode } from "react";
import { useEffect } from "react";
import { useNavigate, useParams } from "react-router";

interface ProtectedLayoutProps {
	children: ReactNode;
}

export function ProtectedLayout({ children }: ProtectedLayoutProps) {
	const navigate = useNavigate();
	const { orgId: orgParam } = useParams();
	const { logout, user } = useAuth();
	const { isLoading, error } = useQuery(whoAmI, {});

	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? "" },
		{ enabled: !!user },
	);
	const orgs = orgsRes?.orgs ?? [];

	const activeOrgId = orgParam ?? orgs[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		activeOrgId ? { orgId: activeOrgId } : undefined,
		{ enabled: !!activeOrgId },
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	useEffect(() => {
		if (unauthenticated) {
			void logout();
			void navigate("/login", { replace: true });
		}
	}, [unauthenticated, logout, navigate]);

	if (isLoading || unauthenticated) {
		return <RouteFallback />;
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
