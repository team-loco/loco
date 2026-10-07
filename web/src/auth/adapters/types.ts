export type LoginMethod =
	| { kind: "oauth"; provider: string; label: string }
	| { kind: "email" }
	| { kind: "sso" }
	| { kind: "redirect"; label: string };

export interface AuthAdapter {
	readonly kind: "supabase" | "oidc";
	hasSession: () => boolean;
	subscribe: (onChange: () => void) => () => void;
	getAccessToken: (forceRefresh?: boolean) => Promise<string | null>;
	loginMethods: () => Promise<LoginMethod[]>;
	signInWithOAuth: (provider: string, redirectTo: string) => Promise<void>;
	signInWithEmail: (email: string, redirectTo: string) => Promise<void>;
	signInWithSSO: (domain: string, redirectTo: string) => Promise<void>;
	signInWithRedirect: (redirectTo: string) => Promise<void>;
	completeRedirect: (url: URL) => Promise<void>;
	signOut: () => Promise<void>;
}
