import { useEffect } from "react";
import { useNavigate } from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useAuth } from "@/auth/AuthProvider";

export function DashboardRedirect() {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { isLoading } = useAuth();

	useEffect(() => {
		if (!isLoading && activeOrgId && activeWorkspaceId) {
			navigate(
				`/org/${activeOrgId.toString()}/wks/${activeWorkspaceId.toString()}`
			);
		}
	}, [isLoading, activeOrgId, activeWorkspaceId, navigate]);

	return (
		<div className="flex items-center justify-center min-h-screen bg-background">
			<div className="text-center">
				<div className="w-8 h-8 bg-main rounded-lg mx-auto mb-4 animate-pulse"></div>
				<p className="text-foreground font-base">Loading workspace...</p>
			</div>
		</div>
	);
}
