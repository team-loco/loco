import Loader from "@/assets/loader.svg?react";
import { useAuth } from "@/auth/AuthProvider";
import { SiteHeader } from "@/components/site-header";
import { ContextProvider } from "@/context/ContextProvider";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import "@/styles/dot-grid.css";
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

	// Handle auth failures by redirecting to login
	useEffect(() => {
		if (error) {
			void logout();
			void navigate("/login", { replace: true });
		}
	}, [error, logout, navigate]);

	// Loading user data
	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-screen bg-background">
				<div className="text-center">
					<Loader className="w-12 h-12 mx-auto mb-4" />
				</div>
			</div>
		);
	}

	return (
		<ContextProvider availableOrgs={orgs} availableWorkspaces={workspaces}>
			<div className="flex flex-col w-full min-h-screen">
				<SiteHeader />
				<main
					className="flex-1 w-full overflow-y-auto py-4 flex justify-center dot-grid bg-background"
					style={{ marginTop: "50px" }}
				>
					<div className="w-full">{children}</div>
				</main>
			</div>
		</ContextProvider>
	);
}
