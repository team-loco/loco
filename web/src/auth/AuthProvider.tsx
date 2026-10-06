import { createContext, use, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { whoAmI, logout as logoutMethod } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";

interface AuthContextType {
	user: User | null;
	isAuthenticated: boolean;
	isLoading: boolean;
	error: Error | null;
	logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
	const {
		data: user,
		isLoading,
		error,
	} = useQuery(
		whoAmI,
		{},
		{
			enabled: !window.location.pathname.includes("/oauth/callback"),
		}
	);
	const queryClient = useQueryClient();
	const { mutateAsync: performLogout } = useMutation(logoutMethod);
	const unauthenticated = error instanceof ConnectError && error.code === Code.Unauthenticated;

	const logout = async () => {
		try {
			await performLogout({});
		} catch (err) {
			console.error("Logout failed:", err);
		} finally {
			queryClient.clear();
		}
	};

	return (
		<AuthContext
			value={{
				user: user?.user ?? null,
				isAuthenticated: !unauthenticated && !!user?.user,
				isLoading,
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
