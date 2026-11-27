import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, test } from "bun:test";
import { ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

import { signInError, signInErrorState } from "./sign-in-error";

function failure(reason: string, domain = "loco.auth") {
	return new ConnectError("Could not sign you in", Code.Unauthenticated, undefined, [
		{ desc: ErrorInfoSchema, value: create(ErrorInfoSchema, { reason, domain }) },
	]);
}

function message(error: unknown, fallback: string) {
	return signInError(signInErrorState(error, fallback))?.message;
}

describe("sign-in failures", () => {
	test("has its own text for each reason the API names", () => {
		expect(message(failure("SIGN_IN_CANCELED"), "Sign-in failed")).toBe("Sign-in was canceled.");
		expect(message(failure("SIGNUP_CLOSED"), "Sign-in failed")).toContain("invite-only");
		expect(message(failure("SIGN_IN_EXPIRED"), "Sign-in failed")).toContain("expired");
		expect(message(failure("SIGN_IN_INVALID"), "Sign-in failed")).toContain("could not be verified");
	});

	test("falls back to the error's message for an unknown reason or another domain", () => {
		expect(message(failure("OTHER"), "Sign-in failed")).toBe("Could not sign you in.");
		expect(message(failure("SIGN_IN_CANCELED", "loco.build"), "Sign-in failed")).toBe("Could not sign you in.");
	});
});

test("preserves suspension across the callback redirect only for the auth domain", () => {
	expect(signInError(signInErrorState(failure("ACCOUNT_SUSPENDED"), "Sign-in failed"))?.reason).toBe(
		"ACCOUNT_SUSPENDED",
	);
	expect(
		signInError(signInErrorState(failure("ACCOUNT_SUSPENDED", "loco.build"), "Sign-in failed"))?.reason,
	).toBeNull();
	expect(signInError(null)).toBeNull();
	expect(signInError({ signInError: { message: "failure", reason: 123 } })).toBeNull();
});
