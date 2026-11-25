import { useLocation } from "react-router";

import { inSystem, systemAccess } from "@/lib/system";

import { useMyScopes } from "./useMyScopes";

/** Whether the page is a System page the caller may see; the System chrome never shows to anyone else. */
export function useInSystem(): boolean {
	const { pathname } = useLocation();
	const { scopes } = useMyScopes();
	return inSystem(pathname) && systemAccess(scopes) !== "none";
}
