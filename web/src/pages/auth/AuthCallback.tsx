import { landingPath } from "@/auth/landing";
import { authClient, signOut } from "@/auth/connect-transport";
import { safeNextPath } from "@/auth/next";
import { setClaimedOrgs } from "@/auth/claimed";

import { AuthStep, type AuthStepRun } from "./AuthStep";

const completeSignIn: AuthStepRun = async (transport, queryClient) => {
	const { claimedOrgs, redirectTo } = await authClient.completeWebLogin({ callbackUrl: window.location.href });
	queryClient.clear();
	setClaimedOrgs(claimedOrgs);
	try {
		return await landingPath(transport, queryClient, safeNextPath(redirectTo));
	} catch (err) {
		await signOut().catch(() => undefined);
		throw err;
	}
};

export function AuthCallback() {
	return <AuthStep id="callback" run={completeSignIn} fallbackError="Sign-in failed" />;
}
