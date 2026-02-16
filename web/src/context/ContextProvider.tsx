import { createContext, useContext, type ReactNode, useEffect, useMemo, useState } from "react";
import { useParams, useNavigate } from "react-router";
import type { Organization } from "@/gen/loco/org/v1/org_pb";
import type { Workspace } from "@/gen/loco/workspace/v1/workspace_pb";

const ORG_STORAGE_KEY = "loco_active_org_id";
const WORKSPACE_STORAGE_KEY = "loco_active_workspace_id";

interface OrgWorkspaceContextType {
	activeOrgId: bigint | null;
	activeWorkspaceId: bigint | null;
	orgs: Organization[];
	workspaces: Workspace[];
	setActiveOrg: (orgId: bigint) => void;
	setActiveWorkspace: (workspaceId: bigint) => void;
	setOrgs: (orgs: Organization[]) => void;
	setWorkspaces: (workspaces: Workspace[]) => void;
	addOrg: (org: Organization) => void;
	addWorkspace: (workspace: Workspace) => void;
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

	// Manage orgs and workspaces in state
	const [orgs, setOrgsState] = useState<Organization[]>(availableOrgs);
	const [workspaces, setWorkspacesState] = useState<Workspace[]>(availableWorkspaces);

	// Update state when props change
	useEffect(() => {
		setOrgsState(availableOrgs);
	}, [availableOrgs]);

	useEffect(() => {
		setWorkspacesState(availableWorkspaces);
	}, [availableWorkspaces]);

	// Derive active org ID - URL is canonical source of truth
	const activeOrgId = useMemo(() => {
		if (orgParam) {
			const parsedId = BigInt(orgParam);
			// If we have orgs, verify it exists; otherwise trust the URL
			if (orgs.length === 0 || orgs.some((org) => org.id === parsedId)) {
				return parsedId;
			}
		}

		// Fallback to localStorage
		const storedOrgId = localStorage.getItem(ORG_STORAGE_KEY);
		if (storedOrgId) {
			const parsedId = BigInt(storedOrgId);
			if (orgs.length === 0 || orgs.some((org) => org.id === parsedId)) {
				return parsedId;
			}
		}

		// Final fallback to first available org
		return orgs[0]?.id ?? null;
	}, [orgParam, orgs]);

	// Derive active workspace ID - URL is canonical source of truth
	const activeWorkspaceId = useMemo(() => {
		if (workspaceParam) {
			const parsedId = BigInt(workspaceParam);
			// If we have workspaces, verify it exists; otherwise trust the URL
			if (workspaces.length === 0 || workspaces.some((ws) => ws.id === parsedId)) {
				return parsedId;
			}
		}

		// Fallback to localStorage
		const storedWsId = localStorage.getItem(WORKSPACE_STORAGE_KEY);
		if (storedWsId) {
			const parsedId = BigInt(storedWsId);
			if (workspaces.length === 0 || workspaces.some((ws) => ws.id === parsedId)) {
				return parsedId;
			}
		}

		// Final fallback to first available workspace
		return workspaces[0]?.id ?? null;
	}, [workspaceParam, workspaces]);

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
		const workspace = workspaces.find(
			(ws) => ws.orgId === orgId
		) ?? workspaces[0];
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

	const setOrgs = (newOrgs: Organization[]) => {
		setOrgsState(newOrgs);
	};

	const setWorkspaces = (newWorkspaces: Workspace[]) => {
		setWorkspacesState(newWorkspaces);
	};

	const addOrg = (org: Organization) => {
		setOrgsState((prev) => {
			// Avoid duplicates
			if (prev.some((o) => o.id === org.id)) {
				return prev;
			}
			return [...prev, org];
		});
	};

	const addWorkspace = (workspace: Workspace) => {
		setWorkspacesState((prev) => {
			// Avoid duplicates
			if (prev.some((w) => w.id === workspace.id)) {
				return prev;
			}
			return [...prev, workspace];
		});
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
				orgs,
				workspaces,
				setActiveOrg,
				setActiveWorkspace,
				setOrgs,
				setWorkspaces,
				addOrg,
				addWorkspace,
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
			orgs: [],
			workspaces: [],
			setActiveOrg: () => {},
			setActiveWorkspace: () => {},
			setOrgs: () => {},
			setWorkspaces: () => {},
			addOrg: () => {},
			addWorkspace: () => {},
			clearContext: () => {},
		};
	}
	return ctx;
}
