import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { resourcesPerEnvironment } from "./environments";

function resource(...envIds: string[]) {
	return create(ResourceSchema, { environmentStatuses: envIds.map((environmentId) => ({ environmentId })) });
}

test("resourcesPerEnvironment counts each resource once per environment it runs in", () => {
	const counts = resourcesPerEnvironment([resource("prod", "staging"), resource("prod", "prod"), resource()]);
	expect(counts.get("prod")).toBe(2);
	expect(counts.get("staging")).toBe(1);
	expect(counts.has("dev")).toBe(false);
});
