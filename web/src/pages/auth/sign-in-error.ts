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

/** The message for a failed sign-in: the text for the API's reason when it has one, else the error's own. */
export function signInFailureMessage(error: unknown, fallback: string): string {
	if (error instanceof ConnectError) {
		const info = error.findDetails(ErrorInfoSchema).find((d) => d.domain === SIGN_IN_DOMAIN);
		const message = info === undefined ? undefined : REASON_MESSAGES[info.reason];
		if (message !== undefined) return message;
	}
	return getErrorMessage(error, fallback);
}

export function signInErrorState(message: string): Record<typeof KEY, string> {
	return { [KEY]: message };
}

export function signInError(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !(KEY in state)) return null;
	const message: unknown = state[KEY];
	return typeof message === "string" ? message : null;
}
