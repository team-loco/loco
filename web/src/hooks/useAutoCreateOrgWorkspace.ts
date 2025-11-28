import { useMutation } from "@connectrpc/connect-query";
import { createOrg } from "@/gen/org/v1";
import { createWorkspace } from "@/gen/workspace/v1";
import { useState } from "react";

export function useAutoCreateOrgWorkspace() {
	const [step, setStep] = useState<"idle" | "creating-org" | "creating-workspace" | "done" | "error">("idle");
	const [error, setError] = useState<string | null>(null);
	const [orgId, setOrgId] = useState<string | null>(null);
	const [workspaceId, setWorkspaceId] = useState<string | null>(null);

	const createOrgMutation = useMutation(createOrg);
	const createWorkspaceMutation = useMutation(createWorkspace);

	const autoCreate = async (userEmail: string, userName: string) => {
		try {
			setStep("creating-org");
			setError(null);

			// Create organization with user's email as name
			const orgRes = await createOrgMutation.mutateAsync({
				name: userEmail,
			});

			if (!orgRes.org?.id) {
				throw new Error("Failed to create organization");
			}

			const createdOrgId = orgRes.org.id;
			setOrgId(createdOrgId);

			// Create workspace with user's name
			setStep("creating-workspace");
			const wsRes = await createWorkspaceMutation.mutateAsync({
				orgId: createdOrgId,
				name: `${userName}'s workspace`,
			});

			if (!wsRes.workspace?.id) {
				throw new Error("Failed to create workspace");
			}

			setWorkspaceId(wsRes.workspace.id);
			setStep("done");
			return { orgId: createdOrgId, workspaceId: wsRes.workspace.id };
		} catch (err) {
			const message = err instanceof Error ? err.message : "Failed to auto-create organization and workspace";
			setError(message);
			setStep("error");
			throw err;
		}
	};

	return {
		autoCreate,
		step,
		error,
		orgId,
		workspaceId,
		isLoading: createOrgMutation.isPending || createWorkspaceMutation.isPending,
	};
}
