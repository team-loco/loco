import { createContext, use, useSyncExternalStore, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useLocation } from "react-router";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { ClaimedOrg, User } from "@gen/loco/user/v1/user_pb";

import { toastConnectError } from "@/lib/error-handler";
import { removeStorage } from "@/lib/storage";

import { signOut, unauthenticated } from "./connect-transport";
import { getClaimedOrgs, setClaimedOrgs, subscribeClaimedOrgs } from "./claimed";

interface AuthContextType {
	user: User | null;
	claimedOrgs: ClaimedOrg[];
	dismissClaimedOrgs: () => void;
	signedOut: boolean;
	isAuthenticated: boolean;
	isPending: boolean;
	error: Error | null;
	logout: (returnTo?: "/login") => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export const QUERY_CACHE_KEY = "loco:query-cache:v1";

export function AuthProvider({ children }: { children: ReactNode }) {
	const { pathname } = useLocation();
	const { data, isPending, error } = useQuery(
		whoAmI,
		{},
		{
			enabled: pathname !== "/auth/callback",
			meta: { persist: false },
			staleTime: 0,
			refetchOnWindowFocus: true,
		},
	);
	const queryClient = useQueryClient();
	const claimedOrgs = useSyncExternalStore(subscribeClaimedOrgs, getClaimedOrgs);
	const signedOut = unauthenticated(error);
	const user = signedOut ? null : (data?.user ?? null);

	const logout = async (returnTo: "/" | "/login" = "/") => {
		try {
			await signOut();
		} catch (err) {
			toastConnectError(err, "Could not sign out");
			return;
		}
		queryClient.clear();
		removeStorage(QUERY_CACHE_KEY);
		window.location.replace(returnTo);
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
				isAuthenticated: user !== null,
				isPending,
				error: signedOut ? null : error,
				logout,
			}}
		>
			{children}
		</AuthContext>
	);
}

export function useAuth() {
	const ctx = use(AuthContext);
	if (!ctx) {
		throw new Error("useAuth must be used inside AuthProvider");
	}
	return ctx;
}
