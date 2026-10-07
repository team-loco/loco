import { isEmailLinkType } from "@/auth/adapters/types";
import { landingPath } from "@/auth/landing";
import { authAdapter } from "@/auth/runtime";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const confirmEmailLink: AuthStepRun = async (transport, report) => {
	const params = new URLSearchParams(window.location.search);
	const tokenHash = params.get("token_hash");
	const type = params.get("type");
	if (tokenHash === null || !isEmailLinkType(type)) throw new Error("This email link is incomplete");
	const adapter = await authAdapter();
	if (adapter === null) throw new Error("Sign-in is not configured");
	report(type === "email_change" ? "Confirming your email…" : "Signing you in…");
	await adapter.verifyEmailLink(tokenHash, type);
	if (type === "email_change") return "/profile";
	return await landingPath(transport, null, report);
};

export function AuthConfirm() {
	return <AuthStep id="confirm" run={confirmEmailLink} fallbackError="This email link is invalid or has expired" />;
}
