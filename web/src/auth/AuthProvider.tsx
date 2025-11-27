import { createContext, useContext, useState, type ReactNode } from "react";

interface AuthContextType {
	token: string | null;
	login: () => void;
	logout: () => void;
	isAuthenticated: boolean;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
	// With cookie-based auth, we just need to know if user is authenticated
	// The backend will verify the cookie when we make requests
	// A non-empty token indicates successful login
	const [token, setTokenState] = useState<string | null>("cookie-based");

	const login = () => {
		// Token is stored in HTTP-only cookie, not in state
		setTokenState("cookie-based");
		console.log("AuthProvider: login successful (token in cookie)");
	};

	const logout = () => {
		// Clear the cookie by calling logout endpoint
		fetch("http://localhost:8000/logout", { method: "POST" }).catch(() => {});
		setTokenState(null);
		console.log("AuthProvider: logout");
	};

	return (
		<AuthContext.Provider
			value={{
				token,
				login,
				logout,
				isAuthenticated: !!token,
			}}
		>
			{children}
		</AuthContext.Provider>
	);
}

export function useAuth() {
	const ctx = useContext(AuthContext);
	if (!ctx) {
		throw new Error("useAuth must be used inside AuthProvider");
	}
	return ctx;
}
