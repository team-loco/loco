import { createContext, use } from "react";
import type { ClaimedOrg, User } from "@gen/loco/user/v1/user_pb";

interface AuthContextType {
	user: User | null;
	claimedOrgs: ClaimedOrg[];
	dismissClaimedOrgs: () => void;
	signedOut: boolean;
	/** While held, a session that ends sends the user to a sign-in page that waits for a click. */
	signInHeld: boolean;
	holdSignIn: (held: boolean) => void;
	isAuthenticated: boolean;
	isPending: boolean;
	error: Error | null;
	logout: () => Promise<void>;
}

export const AuthContext = createContext<AuthContextType | null>(null);

export function useAuth() {
	const ctx = use(AuthContext);
	if (!ctx) {
		throw new Error("useAuth must be used inside AuthProvider");
	}
	return ctx;
}
