import { createContext, use, useSyncExternalStore, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";

import { authClient } from "./client";
import type { AuthClient } from "./oidc";

interface AuthContextType {
	user: User | null;
	client: AuthClient | null;
	signedOut: boolean;
	isAuthenticated: boolean;
	isPending: boolean;
	error: Error | null;
	logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

const clientForUI: Promise<AuthClient | null> = authClient().catch((err: unknown) => {
	console.error("Could not load the sign-in configuration:", err);
	return null;
});

const SIGN_IN_PATHS = ["/auth/callback"];

const noSubscription = () => () => undefined;
const neverSignedIn = () => false;

export function AuthProvider({ children }: { children: ReactNode }) {
	const client = use(clientForUI);
	const hasSession = useSyncExternalStore(
		client?.subscribe ?? noSubscription,
		client?.hasSession ?? neverSignedIn,
	);
	const onSignInPage = SIGN_IN_PATHS.some((path) => window.location.pathname.startsWith(path));
	const {
		data: user,
		isPending,
		error,
	} = useQuery(whoAmI, {}, { enabled: hasSession && !onSignInPage });
	const queryClient = useQueryClient();
	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	const logout = async () => {
		try {
			await client?.signOut();
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
				client,
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
