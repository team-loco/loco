import { createContext, use, useSyncExternalStore, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { whoAmI, logout as logoutMethod } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";

import type { AuthAdapter } from "./adapters/types";
import { authAdapter } from "./runtime";

interface AuthContextType {
	user: User | null;
	adapter: AuthAdapter | null;
	signedOut: boolean;
	isAuthenticated: boolean;
	isPending: boolean;
	error: Error | null;
	logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

const adapterForUI: Promise<AuthAdapter | null> = authAdapter().catch((err: unknown) => {
	console.error("Could not load the sign-in configuration:", err);
	return null;
});

const SIGN_IN_PATHS = ["/oauth/callback", "/auth/callback", "/auth/confirm"];

const noSubscription = () => () => undefined;
const alwaysSignedIn = () => true;

export function AuthProvider({ children }: { children: ReactNode }) {
	const adapter = use(adapterForUI);
	const hasSession = useSyncExternalStore(
		adapter?.subscribe ?? noSubscription,
		adapter?.hasSession ?? alwaysSignedIn,
	);
	const onSignInPage = SIGN_IN_PATHS.some((path) => window.location.pathname.startsWith(path));
	const {
		data: user,
		isPending,
		error,
	} = useQuery(whoAmI, {}, { enabled: hasSession && !onSignInPage });
	const queryClient = useQueryClient();
	const { mutateAsync: performLogout } = useMutation(logoutMethod);
	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	const logout = async () => {
		try {
			if (adapter === null) await performLogout({});
			else await adapter.signOut();
		} catch (err) {
			console.error("Logout failed:", err);
		} finally {
			queryClient.clear();
		}
	};

	return (
		<AuthContext
			value={{
				user: hasSession ? (user?.user ?? null) : null,
				adapter,
				signedOut: !hasSession,
				isAuthenticated: hasSession && !unauthenticated && !!user?.user,
				isPending: hasSession && isPending,
				error: error instanceof Error ? error : null,
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
