import { landingPath } from "@/auth/landing";
import { authAdapter } from "@/auth/adapter";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const completeSignIn: AuthStepRun = async (transport, report) => {
	const adapter = await authAdapter();
	if (adapter === null) throw new Error("Sign-in is not configured");
	await adapter.completeRedirect(new URL(window.location.href));
	return await landingPath(transport, null, report);
};

export function AuthCallback() {
	return <AuthStep id="callback" run={completeSignIn} fallbackError="Sign-in failed" />;
}
