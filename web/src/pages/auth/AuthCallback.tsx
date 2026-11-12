import { landingPath } from "@/auth/landing";
import { authClient } from "@/auth/client";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const completeSignIn: AuthStepRun = async (transport, queryClient) => {
	const client = await authClient();
	await client.completeSignIn(new URL(window.location.href));
	try {
		return await landingPath(transport, queryClient);
	} catch (err) {
		await client.signOut();
		throw err;
	}
};

export function AuthCallback() {
	return <AuthStep id="callback" run={completeSignIn} fallbackError="Sign-in failed" />;
}
