import { Code, ConnectError } from "@connectrpc/connect";
import { createOrg, listUserOrgs } from "@/gen/loco/org/v1";
import { createWorkspace, listOrgWorkspaces } from "@/gen/loco/workspace/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useCallback, useState } from "react";
import { useAuth } from "@/auth/AuthProvider";

export function useAutoCreateOrgWorkspace() {
	const { user } = useAuth();
	const [step, setStep] = useState<
		"idle" | "creating-org" | "creating-workspace" | "done" | "error"
	>("idle");
	const [error, setError] = useState<string | null>(null);
	const [orgId, setOrgId] = useState<bigint | null>(null);
	const [workspaceId, setWorkspaceId] = useState<bigint | null>(null);

	const createOrgMutation = useMutation(createOrg);
	const createWorkspaceMutation = useMutation(createWorkspace);
	const { data: userOrgsData, refetch: refetchOrgs } = useQuery(
		listUserOrgs,
		user ? { userId: BigInt(user.id) } : undefined,
		{ enabled: !!user }
	);
	const [wsQueryOrgId, setWsQueryOrgId] = useState<bigint>(0n);
	const { refetch: refetchWorkspaces } = useQuery(
		listOrgWorkspaces,
		{ orgId: wsQueryOrgId },
		{ enabled: false }
	);

	const autoCreate = useCallback(
		async (userEmail: string) => {
			try {
				setStep("creating-org");
				setError(null);

				let createdOrgId: bigint;

				// Try to create organization with user's email as name
				try {
					const org = await createOrgMutation.mutateAsync({
						name: userEmail,
					});

					if (!org?.orgId) {
						throw new Error("Failed to create organization");
					}

					createdOrgId = org.orgId;
				} catch (createErr) {
					// If org already exists, fetch it instead
					if (
						createErr instanceof ConnectError &&
						createErr.code === Code.AlreadyExists
					) {
						const orgsRes = await refetchOrgs();
						const existingOrg = orgsRes.data?.orgs?.[0];
						if (!existingOrg?.id) {
							throw new Error(
								"Organization already exists but could not be retrieved"
							);
						}
						createdOrgId = existingOrg.id;
					} else {
						throw createErr;
					}
				}

				setOrgId(createdOrgId);

				// Create workspace with user's name
				setStep("creating-workspace");
				let createdWorkspaceId: bigint;

				try {
					const workspace = await createWorkspaceMutation.mutateAsync({
						orgId: createdOrgId,
						name: "default",
					});

					if (!workspace?.workspaceId) {
						throw new Error("Failed to create workspace");
					}

					createdWorkspaceId = workspace.workspaceId;
				} catch (wsErr) {
					// If workspace already exists, fetch it instead
					if (
						wsErr instanceof ConnectError &&
						wsErr.code === Code.AlreadyExists
					) {
						setWsQueryOrgId(createdOrgId);
						const wsRes = await refetchWorkspaces();
						const existingWorkspace = wsRes.data?.workspaces?.[0];
						if (!existingWorkspace?.id) {
							throw new Error(
								"Workspace already exists but could not be retrieved"
							);
						}
						createdWorkspaceId = existingWorkspace.id;
					} else {
						throw wsErr;
					}
				}

				setWorkspaceId(createdWorkspaceId);
				setStep("done");
				return { orgId: createdOrgId, workspaceId: createdWorkspaceId };
			} catch (err) {
				const message =
					err instanceof Error
						? err.message
						: "Failed to auto-create organization and workspace";
				setError(message);
				setStep("error");
				throw err;
			}
		},
		[createOrgMutation, createWorkspaceMutation, refetchOrgs, refetchWorkspaces]
	);

	const hasOrgs = userOrgsData?.orgs && userOrgsData.orgs.length > 0;

	return {
		autoCreate,
		step,
		error,
		orgId,
		workspaceId,
		isLoading: createOrgMutation.isPending || createWorkspaceMutation.isPending,
		shouldAutoCreate: !hasOrgs && !!user,
	};
}
