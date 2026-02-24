import { useAuth } from "@/auth/AuthProvider";
import { SiteHeader } from "@/components/site-header";
import { ContextProvider } from "@/context/ContextProvider";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { whoAmI } from "@/gen/loco/user/v1";
import { listOrgWorkspaces } from "@/gen/loco/workspace/v1";
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

	const activeOrgId = orgParam ? orgParam : (orgs[0]?.id ?? null);

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		activeOrgId ? { orgId: activeOrgId } : undefined,
		{ enabled: !!activeOrgId },
	);
	const workspaces = workspacesRes?.workspaces ?? [];

	// Handle auth failures by redirecting to login
	useEffect(() => {
		if (error) {
			logout();
			navigate("/login", { replace: true });
		}
	}, [error, logout, navigate]);

	// Loading user data
	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-screen bg-background">
				<div className="text-center">
					<div className="w-8 h-8 bg-main rounded-lg mx-auto mb-4 animate-pulse"></div>
					<p className="text-foreground font-base">Loading Loco...</p>
				</div>
			</div>
		);
	}

	return (
		<ContextProvider availableOrgs={orgs} availableWorkspaces={workspaces}>
			<div className="flex flex-col w-full min-h-screen">
				<SiteHeader />
				<main className="flex-1 w-full overflow-y-auto px-4 py-4 flex justify-center dot-grid bg-background" style={{ marginTop: "44px" }}>
					<div className="w-full">{children}</div>
				</main>
			</div>
		</ContextProvider>
	);
}
