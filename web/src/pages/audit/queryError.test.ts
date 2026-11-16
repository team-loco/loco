import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { BadRequestSchema, ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";

import { auditQuery } from "./model";
import { auditSearchError, insertAuditField } from "./queryError";

function rejection(position: number) {
	return new ConnectError("Unknown field", Code.InvalidArgument, undefined, [
		{
			desc: BadRequestSchema,
			value: create(BadRequestSchema, { fieldViolations: [{ field: "query", description: "Unknown field" }] }),
		},
		{
			desc: ErrorInfoSchema,
			value: create(ErrorInfoSchema, { reason: "QUERY_SYNTAX", metadata: { position: position.toString() } }),
		},
	]);
}

describe("audit search errors", () => {
	test("does not reject text without a server syntax error", () => {
		expect(auditSearchError(new Error("unknown"), [], "", "@unknown:value")).toBeNull();
		expect(auditSearchError(new ConnectError("bad", Code.InvalidArgument), [], "", "@unknown:value")).toBeNull();
	});
	test("maps server positions past filters, grouping and leading spaces", () => {
		const search = "  hello OR @unknown:value  ";
		const types = ["resource.created"];
		const actor = "person";
		const sent = auditQuery(types, actor, search);
		const position = Array.from(sent.slice(0, sent.indexOf("@unknown"))).length + 1;
		const result = auditSearchError(rejection(position), types, actor, search);
		expect(result?.error.query).toBe(search);
		expect(result?.start).toBe(11);
		expect(result?.end).toBe(25);
		expect(result?.error.position).toBe(12);
	});
	test("highlights a whole word when the server points inside it", () => {
		const result = auditSearchError(rejection(8), [], "", "@request:foo:bar");
		expect(result?.start).toBe(0);
		expect(result?.end).toBe(16);
	});
	test("counts Unicode characters and inserts with a UTF-16 input caret", () => {
		const search = "🚀 @unknown:value tail";
		const result = auditSearchError(rejection(3), [], "", search);
		expect(result?.start).toBe(2);
		expect(result?.end).toBe(16);
		expect(insertAuditField(search, 2, 16, "actor")).toEqual({ query: "🚀 @actor: tail", caret: 10 });
	});
	test("inserts at the end when the server expects another term", () => {
		const result = auditSearchError(rejection(6), [], "", "hello");
		expect(result?.start).toBe(5);
		expect(result?.end).toBe(5);
		expect(insertAuditField("hello OR", 8, 8, "type")).toEqual({ query: "hello OR @type:", caret: 15 });
	});
});
