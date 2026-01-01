import { createContext, useContext, type ReactNode, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUser, logout as logoutMethod } from "@/gen/user/v1";
import type { GetCurrentUserResponse } from "@/gen/user/v1/user_pb";

interface AuthContextType {
	user: GetCurrentUserResponse | null;
	isAuthenticated: boolean;
	isLoading: boolean;
	error: Error | null;
	logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
	const { data: user, isLoading, error } = useQuery(getCurrentUser, {});
	const [isLoggedOut, setIsLoggedOut] = useState(false);

	const { refetch: performLogout } = useQuery(
		logoutMethod,
		{},
		{ enabled: false }
	);

	const logout = async () => {
		try {
			await performLogout();
			setIsLoggedOut(true);
		} catch (err) {
			console.error("Logout failed:", err);
		}
	};

	return (
		<AuthContext.Provider
			value={{
				user: user ?? null,
				isAuthenticated: !isLoggedOut && !!user?.user,
				isLoading,
				error: error instanceof Error ? error : null,
				logout,
			}}
		>
			{children}
		</AuthContext.Provider>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth() {
	const ctx = useContext(AuthContext);
	if (!ctx) {
		throw new Error("useAuth must be used inside AuthProvider");
	}
	return ctx;
}
