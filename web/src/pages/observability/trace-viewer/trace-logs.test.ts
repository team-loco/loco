import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { summarizeTraces } from "../traces";
import { descendantIds } from "./trace-logs";

test("span log scope includes descendants and excludes ancestors and siblings", () => {
	const rows = (
		[
			["root", ""],
			["selected", "root"],
			["child", "selected"],
			["sibling", "root"],
		] as const
	).map(([spanId, parentSpanId]) =>
		create(TraceSpanSchema, { traceId: "trace", spanId, parentSpanId, durationNanos: 1n }),
	);
	const trace = summarizeTraces(rows)[0];
	if (trace === undefined) throw new Error("Missing fixture trace");
	expect([...descendantIds(trace, "selected")]).toEqual(["selected", "child"]);
});
