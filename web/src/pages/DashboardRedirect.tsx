import { Navigate } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { AppLoading } from "@/context/AppLoader";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspacePath } from "@/lib/routes";

export function DashboardRedirect() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { isLoading } = useAuth();

	if (!isLoading && activeOrgId !== null && activeWorkspaceId !== null) {
		return <Navigate to={workspacePath(activeOrgId, activeWorkspaceId)} replace />;
	}

	return <AppLoading />;
}
