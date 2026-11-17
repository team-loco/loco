import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { ActivitySchema } from "@gen/loco/deployment/v1/deployment_pb";

import { mergeActivity } from "./deployment-watch";

test("replayed and overlapping activity batches preserve each position once", () => {
	const a = (seq: bigint) => create(ActivitySchema, { seq, message: `event ${seq}` });
	const previous = [a(1n), a(2n)];
	expect(mergeActivity(previous, [a(2n), a(3n)]).map((entry) => entry.seq)).toEqual([1n, 2n, 3n]);
	expect(mergeActivity(previous, []).map((entry) => entry.seq)).toEqual([1n, 2n]);
	expect(mergeActivity([], [a(4n), a(3n)]).map((entry) => entry.seq)).toEqual([3n, 4n]);
});
