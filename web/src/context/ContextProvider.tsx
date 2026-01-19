import { createContext, useContext, type ReactNode, useEffect, useMemo } from "react";
import { useParams, useNavigate } from "react-router";
import type { Organization } from "@/gen/loco/org/v1/org_pb";
import type { Workspace } from "@/gen/loco/workspace/v1/workspace_pb";

const ORG_STORAGE_KEY = "loco_active_org_id";
const WORKSPACE_STORAGE_KEY = "loco_active_workspace_id";

interface OrgWorkspaceContextType {
	activeOrgId: bigint | null;
	activeWorkspaceId: bigint | null;
	setActiveOrg: (orgId: bigint) => void;
	setActiveWorkspace: (workspaceId: bigint) => void;
	clearContext: () => void;
}

const OrgWorkspaceContext = createContext<OrgWorkspaceContextType | null>(null);

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

	// Derive active org ID - URL is canonical source of truth
	const activeOrgId = useMemo(() => {
		if (orgParam) {
			const parsedId = BigInt(orgParam);
			// If we have available orgs, verify it exists; otherwise trust the URL
			if (availableOrgs.length === 0 || availableOrgs.some((org) => org.id === parsedId)) {
				return parsedId;
			}
		}

		// Fallback to localStorage
		const storedOrgId = localStorage.getItem(ORG_STORAGE_KEY);
		if (storedOrgId) {
			const parsedId = BigInt(storedOrgId);
			if (availableOrgs.length === 0 || availableOrgs.some((org) => org.id === parsedId)) {
				return parsedId;
			}
		}

		// Final fallback to first available org
		return availableOrgs[0]?.id ?? null;
	}, [orgParam, availableOrgs]);

	// Derive active workspace ID - URL is canonical source of truth
	const activeWorkspaceId = useMemo(() => {
		if (workspaceParam) {
			const parsedId = BigInt(workspaceParam);
			// If we have available workspaces, verify it exists; otherwise trust the URL
			if (availableWorkspaces.length === 0 || availableWorkspaces.some((ws) => ws.id === parsedId)) {
				return parsedId;
			}
		}

		// Fallback to localStorage
		const storedWsId = localStorage.getItem(WORKSPACE_STORAGE_KEY);
		if (storedWsId) {
			const parsedId = BigInt(storedWsId);
			if (availableWorkspaces.length === 0 || availableWorkspaces.some((ws) => ws.id === parsedId)) {
				return parsedId;
			}
		}

		// Final fallback to first available workspace
		return availableWorkspaces[0]?.id ?? null;
	}, [workspaceParam, availableWorkspaces]);

	// Persist active org to localStorage whenever it changes
	useEffect(() => {
		if (activeOrgId) {
			localStorage.setItem(ORG_STORAGE_KEY, activeOrgId.toString());
		}
	}, [activeOrgId]);

	// Persist active workspace to localStorage whenever it changes
	useEffect(() => {
		if (activeWorkspaceId) {
			localStorage.setItem(WORKSPACE_STORAGE_KEY, activeWorkspaceId.toString());
		}
	}, [activeWorkspaceId]);

	const setActiveOrg = (orgId: bigint) => {
		// Navigate to the org with its first available workspace
		const workspace = availableWorkspaces.find(
			(ws) => ws.orgId === orgId
		) ?? availableWorkspaces[0];
		if (workspace) {
			navigate(`/org/${orgId.toString()}/wks/${workspace.id.toString()}`);
		} else {
			navigate(`/org/${orgId.toString()}/wks/select`);
		}
	};

	const setActiveWorkspace = (workspaceId: bigint) => {
		if (activeOrgId) {
			navigate(
				`/org/${activeOrgId.toString()}/wks/${workspaceId.toString()}`
			);
		}
	};

	const clearContext = () => {
		localStorage.removeItem(ORG_STORAGE_KEY);
		localStorage.removeItem(WORKSPACE_STORAGE_KEY);
		navigate("/organizations");
	};

	return (
		<OrgWorkspaceContext.Provider
			value={{
				activeOrgId,
				activeWorkspaceId,
				setActiveOrg,
				setActiveWorkspace,
				clearContext,
			}}
		>
			{children}
		</OrgWorkspaceContext.Provider>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useOrgWorkspace() {
	const ctx = useContext(OrgWorkspaceContext);
	if (!ctx) {
		// Return null values when not in provider (e.g., on public pages)
		return {
			activeOrgId: null,
			activeWorkspaceId: null,
			setActiveOrg: () => {},
			setActiveWorkspace: () => {},
			clearContext: () => {},
		};
	}
	return ctx;
}
