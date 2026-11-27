import { ConnectError } from "@connectrpc/connect";
import { ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

import { getErrorMessage } from "@/lib/error-handler";

const KEY = "signInError";
const SIGN_IN_DOMAIN = "loco.auth";

const REASON_MESSAGES: Record<string, string> = {
	SIGN_IN_CANCELED: "Sign-in was canceled.",
	SIGNUP_CLOSED: "Loco is invite-only right now. Ask for an invitation, then sign in again.",
	SIGN_IN_EXPIRED: "This sign-in link expired. Start again.",
	SIGN_IN_INVALID: "This sign-in could not be verified. Start again.",
};

type SignInFailure = { message: string; reason: string | null };

export function signInErrorState(error: unknown, fallback: string): Record<typeof KEY, SignInFailure> {
	const info =
		error instanceof ConnectError
			? error.findDetails(ErrorInfoSchema).find((d) => d.domain === SIGN_IN_DOMAIN)
			: undefined;
	const reason = info?.reason ?? null;
	const message = reason === null ? undefined : REASON_MESSAGES[reason];
	return { [KEY]: { message: message ?? getErrorMessage(error, fallback), reason } };
}

export function signInError(state: unknown): SignInFailure | null {
	if (typeof state !== "object" || state === null) return null;
	if (!(KEY in state)) return null;
	const failure: unknown = state[KEY];
	if (typeof failure !== "object" || failure === null) return null;
	if (!("message" in failure) || typeof failure.message !== "string") return null;
	if (!("reason" in failure)) return null;
	if (failure.reason !== null && typeof failure.reason !== "string") return null;
	return { message: failure.message, reason: failure.reason };
}
