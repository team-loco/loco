import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, test } from "bun:test";
import { ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

import { signInFailureMessage } from "./sign-in-error";

function failure(reason: string, domain = "loco.auth") {
	return new ConnectError("Could not sign you in", Code.Unauthenticated, undefined, [
		{ desc: ErrorInfoSchema, value: create(ErrorInfoSchema, { reason, domain }) },
	]);
}

describe("signInFailureMessage", () => {
	test("has its own text for each reason the API names", () => {
		expect(signInFailureMessage(failure("SIGN_IN_CANCELED"), "Sign-in failed")).toBe("Sign-in was canceled.");
		expect(signInFailureMessage(failure("SIGNUP_CLOSED"), "Sign-in failed")).toContain("invite-only");
		expect(signInFailureMessage(failure("SIGN_IN_EXPIRED"), "Sign-in failed")).toContain("expired");
		expect(signInFailureMessage(failure("SIGN_IN_INVALID"), "Sign-in failed")).toContain("could not be verified");
	});

	test("falls back to the error's message for an unknown reason or another domain", () => {
		expect(signInFailureMessage(failure("OTHER"), "Sign-in failed")).toBe("Could not sign you in.");
		expect(signInFailureMessage(failure("SIGN_IN_CANCELED", "loco.build"), "Sign-in failed")).toBe(
			"Could not sign you in.",
		);
	});
});
