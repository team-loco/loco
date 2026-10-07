import { GoTrueClient, type Provider, type Session, type SupportedStorage } from "@supabase/auth-js";

import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

import type { AuthAdapter, EmailLinkType, LoginMethod } from "./types";

const STORAGE_KEY = "loco:auth:supabase:v1";

const PROVIDER_LABELS: Record<string, string> = {
	github: "GitHub",
	google: "Google",
	gitlab: "GitLab",
	bitbucket: "Bitbucket",
	azure: "Microsoft",
	keycloak: "Keycloak",
};

const NON_OAUTH_SETTINGS = new Set(["email", "phone", "anonymous_users", "saml"]);

const storage: SupportedStorage = {
	getItem: (key) => readStorage(key),
	setItem: (key, value) => {
		writeStorage(key, value);
	},
	removeItem: (key) => {
		removeStorage(key);
	},
};

interface Settings {
	external?: Record<string, boolean>;
	saml_enabled?: boolean;
}

function check(result: { error: Error | null }): void {
	if (result.error !== null) throw result.error;
}

export async function createSupabaseAdapter(url: string): Promise<AuthAdapter> {
	const client = new GoTrueClient({
		url,
		storage,
		storageKey: STORAGE_KEY,
		persistSession: true,
		autoRefreshToken: true,
		detectSessionInUrl: false,
		flowType: "pkce",
	});
	const listeners = new Set<() => void>();
	const initial = await client.getSession();
	check(initial);
	let session: Session | null = initial.data.session;
	client.onAuthStateChange((_event, next) => {
		session = next;
		for (const listener of listeners) listener();
	});

	return {
		kind: "supabase",
		hasSession: () => session !== null,
		subscribe: (onChange) => {
			listeners.add(onChange);
			return () => {
				listeners.delete(onChange);
			};
		},
		getAccessToken: async (forceRefresh = false) => {
			if (forceRefresh) {
				const refreshed = await client.refreshSession();
				return refreshed.data.session?.access_token ?? null;
			}
			const { data } = await client.getSession();
			return data.session?.access_token ?? null;
		},
		loginMethods: async () => {
			const res = await fetch(`${url.replace(/\/$/, "")}/settings`);
			if (!res.ok) throw new Error("Could not load sign-in options");
			const settings = (await res.json()) as Settings;
			const external = settings.external ?? {};
			const methods: LoginMethod[] = Object.entries(external)
				.filter(([provider, enabled]) => enabled && !NON_OAUTH_SETTINGS.has(provider))
				.map(([provider]) => ({ kind: "oauth", provider, label: PROVIDER_LABELS[provider] ?? provider }));
			if (external.email === true) methods.push({ kind: "email" });
			if (settings.saml_enabled === true) methods.push({ kind: "sso" });
			return methods;
		},
		signInWithOAuth: async (provider, redirectTo) => {
			check(await client.signInWithOAuth({ provider: provider as Provider, options: { redirectTo } }));
		},
		signInWithEmail: async (email, redirectTo) => {
			check(await client.signInWithOtp({ email, options: { emailRedirectTo: redirectTo } }));
		},
		signInWithSSO: async (domain, redirectTo) => {
			check(await client.signInWithSSO({ domain, options: { redirectTo } }));
		},
		signInWithRedirect: async () => {
			await Promise.reject(new Error("Choose a sign-in method"));
		},
		completeRedirect: async (current) => {
			const description = current.searchParams.get("error_description");
			if (description !== null) throw new Error(description);
			const code = current.searchParams.get("code");
			if (code === null) throw new Error("The sign-in link is missing its code");
			check(await client.exchangeCodeForSession(code));
		},
		verifyEmailLink: async (tokenHash: string, type: EmailLinkType) => {
			check(await client.verifyOtp({ token_hash: tokenHash, type }));
		},
		signOut: async () => {
			const { error } = await client.signOut({ scope: "local" });
			if (error !== null) throw error;
		},
	};
}
