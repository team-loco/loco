import { useState } from "react";

import { toastConnectError } from "@/lib/error-handler";

import { authClient } from "./connect-transport";

const SIGN_IN_FAILED = "Could not start sign-in";

async function startSignIn(next: string | null, loginHint: string | null): Promise<void> {
	const { authorizationUrl } = await authClient.startWebLogin({
		redirectTo: next ?? "",
		loginHint: loginHint ?? "",
	});
	window.location.assign(authorizationUrl);
}

export function useSignIn() {
	const [redirecting, setRedirecting] = useState(false);
	const [failed, setFailed] = useState(false);

	const signIn = async (next: string | null, loginHint: string | null) => {
		setRedirecting(true);
		try {
			await startSignIn(next, loginHint);
		} catch (err) {
			toastConnectError(err, SIGN_IN_FAILED);
			setRedirecting(false);
			setFailed(true);
		}
	};

	return { signIn, redirecting, failed };
}
