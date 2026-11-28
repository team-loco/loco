import { useState, useSyncExternalStore, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useLocation } from "react-router";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";

import { toastConnectError } from "@/lib/error-handler";
import { removeStorage } from "@/lib/storage";

export { useAuth } from "./auth-context";
import { AuthContext } from "./auth-context";

import { signOut, unauthenticated } from "./connect-transport";
import { getClaimedOrgs, setClaimedOrgs, subscribeClaimedOrgs } from "./claimed";

export const QUERY_CACHE_KEY = "loco:query-cache:v1";

export function AuthProvider({ children }: { children: ReactNode }) {
	const { pathname } = useLocation();
	const { data, isPending, error } = useQuery(
		whoAmI,
		{},
		{
			enabled: pathname !== "/auth/callback",
			meta: { persist: false },
		},
	);
	const queryClient = useQueryClient();
	const [signInHeld, setSignInHeld] = useState(false);
	const claimedOrgs = useSyncExternalStore(subscribeClaimedOrgs, getClaimedOrgs);
	const signedOut = unauthenticated(error);
	const user = signedOut ? null : (data?.user ?? null);
	// A refetch that fails keeps the user and the shell; only the first load can fail the account.
	const loadError = signedOut || data !== undefined ? null : error;

	const logout = async () => {
		try {
			await signOut();
		} catch (err) {
			toastConnectError(err, "Could not sign out");
			return;
		}
		queryClient.clear();
		removeStorage(QUERY_CACHE_KEY);
		window.location.replace("/");
	};

	return (
		<AuthContext
			value={{
				user,
				claimedOrgs: user === null ? [] : claimedOrgs,
				dismissClaimedOrgs: () => {
					setClaimedOrgs([]);
				},
				signedOut,
				signInHeld,
				holdSignIn: setSignInHeld,
				isAuthenticated: user !== null,
				isPending,
				error: loadError,
				logout,
			}}
		>
			{children}
		</AuthContext>
	);
}
