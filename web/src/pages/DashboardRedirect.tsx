import { useEffect } from "react";
import { useNavigate } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { AppLoading } from "@/context/AppLoader";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspacePath } from "@/lib/routes";

export function DashboardRedirect() {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { isLoading } = useAuth();

	useEffect(() => {
		if (!isLoading && activeOrgId !== null && activeWorkspaceId !== null) {
			void navigate(workspacePath(activeOrgId, activeWorkspaceId), { replace: true });
		}
	}, [isLoading, activeOrgId, activeWorkspaceId, navigate]);

	return <AppLoading />;
}
