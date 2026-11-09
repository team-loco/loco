import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { SecretSchema } from "@gen/loco/secret/v1/secret_pb";

import { secretRows } from "./variables";

describe("secretRows", () => {
	test("merges stored, declared and staged names with their status", () => {
		const stored = [create(SecretSchema, { name: "API_KEY" }), create(SecretSchema, { name: "OLD_TOKEN" })];
		const rows = secretRows(stored, ["API_KEY", "DATABASE_URL"], ["NEW_ONE"]);
		expect(rows.map((r) => [r.name, r.status])).toEqual([
			["API_KEY", "set"],
			["DATABASE_URL", "missing"],
			["NEW_ONE", "new"],
			["OLD_TOKEN", "unused"],
		]);
	});

	test("leaves the status unknown until the declared names load", () => {
		const rows = secretRows([create(SecretSchema, { name: "API_KEY" })], undefined, []);
		expect(rows.map((r) => r.status)).toEqual([undefined]);
	});
});
