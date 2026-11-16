import { describe, expect, test } from "bun:test";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { BadRequestSchema } from "@gen/google/rpc/error_details_pb";
import { auditFilterViolations, auditLink, sequenceParser } from "./link";

describe("audit links", () => {
	test("keeps usable event types, cursor history, search and page size", () => {
		const result = auditLink(
			"?range=forever&types=resource.created,unknown,secret.set&before=99,nope,55&size=100&q=hello",
			[],
			undefined,
			undefined,
		);
		expect(Object.fromEntries(new URLSearchParams(result.search))).toEqual({
			types: "resource.created,secret.set",
			before: "99,55",
			size: "100",
			q: "hello",
		});
		expect(result.dropped.map((f) => [f.label, f.value])).toEqual([
			["Time range", "forever"],
			["Event", "unknown"],
			["Page", "nope"],
		]);
	});
	test("drops unavailable scope values without discarding a valid workspace", () => {
		const result = auditLink(
			"?ws=shared&res=missing&env=production&size=wat",
			["shared"],
			["checkout"],
			["production"],
		);
		expect(Object.fromEntries(new URLSearchParams(result.search))).toEqual({ ws: "shared", env: "production" });
		expect(result.dropped.map((f) => f.value)).toEqual(["wat", "missing"]);
	});
	test("drops an environment without a workspace", () => {
		expect(auditLink("?env=production", [], undefined, undefined).dropped).toEqual([
			{ label: "Environment", value: "production", reason: "pick a workspace to filter by environment" },
		]);
	});
	test("waits for loaded metadata before treating an ID as missing", () => {
		expect(auditLink("?ws=shared&res=checkout&env=production", undefined, undefined, undefined).dropped).toEqual([]);
		expect(
			auditLink("?ws=missing&res=checkout&env=production", ["shared"], undefined, undefined).dropped.map(
				(f) => f.label,
			),
		).toEqual(["Workspace", "Resource", "Environment"]);
	});
	test("uses the workspace admin's selected workspace when the link omits it", () => {
		expect(auditLink("?res=checkout", ["shared"], ["checkout"], [], "shared").dropped).toEqual([]);
	});
	test("validates cursor positions against the positive int64 protocol", () => {
		for (const value of ["0", "-1", "1.5", "9223372036854775808", "nope"])
			expect(sequenceParser.parse(value)).toBeNull();
		expect(sequenceParser.parse("9223372036854775807")).toBe(9223372036854775807n);
	});
});

test("filter errors require InvalidArgument with non-query field violations", () => {
	const details = [
		{
			desc: BadRequestSchema,
			value: create(BadRequestSchema, {
				fieldViolations: [
					{ field: "query", description: "syntax" },
					{ field: "start_time", description: "outside retention" },
				],
			}),
		},
	];
	expect(
		auditFilterViolations(new ConnectError("bad", Code.InvalidArgument, undefined, details)).map((v) => v.field),
	).toEqual(["start_time"]);
	expect(auditFilterViolations(new ConnectError("bad", Code.Internal, undefined, details))).toEqual([]);
	expect(auditFilterViolations(new Error("bad"))).toEqual([]);
});
