import { useState } from "react";

import { toastConnectError } from "@/lib/error-handler";

import { authClient, signInRedirectURL } from "./client";
import { forgetNextPath, rememberNextPath } from "./next";

const SIGN_IN_FAILED = "Could not start sign-in";

async function startSignIn(next: string | null, loginHint: string | null): Promise<void> {
	if (next === null) forgetNextPath();
	else rememberNextPath(next);
	const client = await authClient();
	await client.signIn(signInRedirectURL(), loginHint);
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
