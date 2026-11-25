import { create } from "@bufbuild/protobuf";
import { textOf } from "@/lib/node-text";
import { expect, test } from "bun:test";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { deleteDescription, resourcesPerEnvironment } from "./environments";

function resource(...envIds: string[]) {
	return create(ResourceSchema, { environmentStatuses: envIds.map((environmentId) => ({ environmentId })) });
}

test("resourcesPerEnvironment counts each resource once per environment it runs in", () => {
	const counts = resourcesPerEnvironment([resource("prod", "staging"), resource("prod", "prod"), resource()]);
	expect(counts.get("prod")).toBe(2);
	expect(counts.get("staging")).toBe(1);
	expect(counts.has("dev")).toBe(false);
});

test("deleteDescription names the secrets that go with the environment", () => {
	expect(textOf(deleteDescription("acme", ["A", "B"]))).toContain("permanently deletes its 2 secrets: A and B.");
});
test("deleteDescription truncates long lists", () => {
	const names = ["A", "B", "C", "D", "E", "F", "G"];
	expect(textOf(deleteDescription("acme", names))).toContain("its 7 secrets: A, B, C, D, E, and 2 more.");
});
test("deleteDescription mentions no secrets when there are none", () => {
	expect(textOf(deleteDescription("acme", []))).not.toContain("secret");
});
