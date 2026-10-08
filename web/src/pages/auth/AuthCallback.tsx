import { landingPath } from "@/auth/landing";
import { authClient } from "@/auth/client";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const completeSignIn: AuthStepRun = async (transport, report) => {
	const client = await authClient();
	await client.completeSignIn(new URL(window.location.href));
	return await landingPath(transport, null, report);
};

export function AuthCallback() {
	return <AuthStep id="callback" run={completeSignIn} fallbackError="Sign-in failed" />;
}
