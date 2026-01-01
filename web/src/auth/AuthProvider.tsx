import { createContext, useContext, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUser, logout as logoutMethod } from "@/gen/user/v1";
import { useNavigate } from "react-router";
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
	const navigate = useNavigate();

	const { data: user, isLoading, error } = useQuery(getCurrentUser, {});

	const { refetch: performLogout } = useQuery(logoutMethod, {}, { enabled: false });

	const logout = async () => {
		try {
			await performLogout();
			navigate("/");
		} catch (err) {
			console.error("Logout failed:", err);
		}
	};

	return (
		<AuthContext.Provider
			value={{
				user: user ?? null,
				isAuthenticated: !!user?.user,
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
