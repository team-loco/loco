export type LoginMethod =
	| { kind: "oauth"; provider: string; label: string }
	| { kind: "email" }
	| { kind: "sso" }
	| { kind: "redirect"; label: string };

export type EmailLinkType = "signup" | "magiclink" | "recovery" | "invite" | "email_change" | "email";

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
	verifyEmailLink: (tokenHash: string, type: EmailLinkType) => Promise<void>;
	signOut: () => Promise<void>;
}

export const EMAIL_LINK_TYPES: readonly EmailLinkType[] = [
	"signup",
	"magiclink",
	"recovery",
	"invite",
	"email_change",
	"email",
];

export function isEmailLinkType(value: string | null): value is EmailLinkType {
	return value !== null && (EMAIL_LINK_TYPES as readonly string[]).includes(value);
}
