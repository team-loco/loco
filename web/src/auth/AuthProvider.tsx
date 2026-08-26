import { createContext, use, type ReactNode, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { whoAmI, logout as logoutMethod } from "@/gen/loco/user/v1";
import type { User } from "@/gen/loco/user/v1/user_pb";

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
			// coming back from oauth we may not have the loco token present.
			enabled: !window.location.pathname.includes("/oauth/callback"),
		}
	);
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
		<AuthContext
			value={{
				user: user?.user ?? null,
				isAuthenticated: !isLoggedOut && !!user?.user,
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
