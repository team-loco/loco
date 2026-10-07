import { landingPath } from "@/auth/landing";
import { authAdapter } from "@/auth/runtime";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const completeSignIn: AuthStepRun = async (transport, report) => {
	const adapter = await authAdapter();
	await adapter.completeRedirect(new URL(window.location.href));
	return await landingPath(transport, null, report);
};

export function AuthCallback() {
	return <AuthStep id="callback" run={completeSignIn} fallbackError="Sign-in failed" />;
}
