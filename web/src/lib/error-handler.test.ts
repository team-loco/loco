import { describe, expect, test } from "bun:test";
import { Code, ConnectError } from "@connectrpc/connect";

import { errorKind, type ErrorKind } from "./error-handler";

describe("errorKind", () => {
	const cases: [Code, ErrorKind | null][] = [
		[Code.Unavailable, "unavailable"],
		[Code.DeadlineExceeded, "unavailable"],
		[Code.Internal, "internal"],
		[Code.Unknown, "internal"],
		[Code.InvalidArgument, "internal"],
		[Code.PermissionDenied, "denied"],
		[Code.ResourceExhausted, "rate"],
		[Code.Unauthenticated, null],
	];
	for (const [code, kind] of cases) {
		test(`${Code[code]} shows ${String(kind)}`, () => {
			expect(errorKind(new ConnectError("failed", code))).toBe(kind);
		});
	}

	test("a fetch that got no response is unavailable", () => {
		expect(errorKind(ConnectError.from(new TypeError("Failed to fetch")))).toBe("unavailable");
	});

	test("an error from outside Connect is internal", () => {
		expect(errorKind(new Error("boom"))).toBe("internal");
	});
});
