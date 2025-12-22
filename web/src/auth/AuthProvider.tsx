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

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth() {
	const ctx = useContext(AuthContext);
	if (!ctx) {
		throw new Error("useAuth must be used inside AuthProvider");
	}
	return ctx;
}
