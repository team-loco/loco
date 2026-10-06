import { createContext, use, useEffect, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

export const ORG_STORAGE_KEY = "loco:active-org:v1";
const WORKSPACE_STORAGE_KEY = "loco:active-workspace:v1";

interface OrgWorkspaceContextType {
	activeOrgId: string | null;
	activeWorkspaceId: string | null;
	orgs: Organization[];
	workspaces: Workspace[];
	setActiveOrg: (orgId: string) => void;
	clearContext: () => void;
}

const NO_CONTEXT: OrgWorkspaceContextType = {
	activeOrgId: null,
	activeWorkspaceId: null,
	orgs: [],
	workspaces: [],
	setActiveOrg: () => undefined,
	clearContext: () => undefined,
};

const OrgWorkspaceContext = createContext<OrgWorkspaceContextType>(NO_CONTEXT);

export function pickActive<T extends { id: string }>(items: T[], fromUrl: string | undefined, storageKey: string): string | null {
	const known = (id: string) => items.length === 0 || items.some((item) => item.id === id);
	if (fromUrl !== undefined && known(fromUrl)) return fromUrl;
	const stored = readStorage(storageKey);
	if (stored !== null && known(stored)) return stored;
	return items[0]?.id ?? null;
}

export function ContextProvider({
	children,
	availableOrgs = [],
	availableWorkspaces = [],
}: {
	children: ReactNode;
	availableOrgs?: Organization[];
	availableWorkspaces?: Workspace[];
}) {
	const { orgId: orgParam, workspaceId: workspaceParam } = useParams();
	const navigate = useNavigate();

	const activeOrgId = pickActive(availableOrgs, orgParam, ORG_STORAGE_KEY);
	const activeWorkspaceId = pickActive(availableWorkspaces, workspaceParam, WORKSPACE_STORAGE_KEY);

	useEffect(() => {
		if (activeOrgId !== null) writeStorage(ORG_STORAGE_KEY, activeOrgId);
		if (activeWorkspaceId !== null) writeStorage(WORKSPACE_STORAGE_KEY, activeWorkspaceId);
	}, [activeOrgId, activeWorkspaceId]);

	const setActiveOrg = (orgId: string) => {
		writeStorage(ORG_STORAGE_KEY, orgId);
		removeStorage(WORKSPACE_STORAGE_KEY);
		void navigate("/dashboard");
	};

	const clearContext = () => {
		removeStorage(ORG_STORAGE_KEY);
		removeStorage(WORKSPACE_STORAGE_KEY);
		void navigate("/organizations");
	};

	return (
		<OrgWorkspaceContext
			value={{
				activeOrgId,
				activeWorkspaceId,
				orgs: availableOrgs,
				workspaces: availableWorkspaces,
				setActiveOrg,
				clearContext,
			}}
		>
			{children}
		</OrgWorkspaceContext>
	);
}

export function useOrgWorkspace(): OrgWorkspaceContextType {
	return use(OrgWorkspaceContext);
}
