import { useEffect, useMemo } from "react";
import { useSearchParams } from "react-router";

const ORG_STORAGE_KEY = "loco_active_org_id";

interface UseOrgContextReturn {
	activeOrgId: bigint | null;
	setActiveOrgId: (orgId: bigint) => void;
	clearActiveOrgId: () => void;
}

export function useOrgContext(
	availableOrgIds: bigint[] = []
): UseOrgContextReturn {
	const [searchParams, setSearchParams] = useSearchParams();

	const activeOrgId = useMemo(() => {
		const orgParam = searchParams.get("org");
		if (orgParam) {
			return BigInt(orgParam);
		}

		// Fallback to localStorage
		const storedOrgId = localStorage.getItem(ORG_STORAGE_KEY);
		if (storedOrgId) {
			const parsedOrgId = BigInt(storedOrgId);
			// Verify the stored org still exists in available orgs
			if (availableOrgIds.some((id) => id === parsedOrgId)) {
				return parsedOrgId;
			}
			// If stored org doesn't exist anymore, clear it
			localStorage.removeItem(ORG_STORAGE_KEY);
		}

		// Final fallback to first available org
		return availableOrgIds[0] ?? null;
	}, [searchParams, availableOrgIds]);

	// Persist active org to localStorage whenever it changes
	useEffect(() => {
		if (activeOrgId) {
			localStorage.setItem(ORG_STORAGE_KEY, activeOrgId.toString());
		}
	}, [activeOrgId]);

	const setActiveOrgId = (orgId: bigint) => {
		const newParams = new URLSearchParams(searchParams);
		newParams.set("org", orgId.toString());
		setSearchParams(newParams);
	};

	const clearActiveOrgId = () => {
		const newParams = new URLSearchParams(searchParams);
		newParams.delete("org");
		setSearchParams(newParams);
		localStorage.removeItem(ORG_STORAGE_KEY);
	};

	return {
		activeOrgId,
		setActiveOrgId,
		clearActiveOrgId,
	};
}
