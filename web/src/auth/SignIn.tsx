import { useEffect, useRef, useState } from "react";
import { Navigate } from "react-router";

import { AppLoading } from "@/context/AppLoader";
import { toastConnectError } from "@/lib/error-handler";

import { authClient, signInRedirectURL } from "./client";
import { forgetNextPath, rememberNextPath } from "./next";

const SIGN_IN_FAILED = "Could not start sign-in";

async function startSignIn(next: string | null): Promise<void> {
	if (next === null) forgetNextPath();
	else rememberNextPath(next);
	const client = await authClient();
	await client.signIn(signInRedirectURL());
}

export function useSignIn() {
	const [redirecting, setRedirecting] = useState(false);

	const signIn = async (next: string | null) => {
		setRedirecting(true);
		try {
			await startSignIn(next);
		} catch (err) {
			toastConnectError(err, SIGN_IN_FAILED);
			setRedirecting(false);
		}
	};

	return { signIn, redirecting };
}

export function SignInRedirect({ next }: { next: string }) {
	const [failed, setFailed] = useState(false);
	const started = useRef(false);

	useEffect(() => {
		if (started.current) return;
		started.current = true;
		startSignIn(next).catch((err: unknown) => {
			toastConnectError(err, SIGN_IN_FAILED);
			setFailed(true);
		});
	}, [next]);

	if (failed) return <Navigate to="/login" replace />;
	return <AppLoading message="Redirecting to sign in…" />;
}
