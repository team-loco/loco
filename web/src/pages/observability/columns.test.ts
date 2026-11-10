import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { columnValue, DEFAULT_COLUMNS, readColumns, writeColumns } from "./columns";
import { toRows } from "./rows";

test("default columns stay out of the URL and custom ones round-trip", () => {
	expect(writeColumns(DEFAULT_COLUMNS)).toBeNull();
	expect(readColumns(null)).toEqual(DEFAULT_COLUMNS);
	const custom = ["level", "replica", "http.method"];
	expect(readColumns(writeColumns(custom))).toEqual(custom);
});

test("attribute columns read log, resource and JSON body attributes", () => {
	const entry = create(LogEntrySchema, {
		body: '{"msg":"done","status":200}',
		logAttributes: { "http.method": "GET" },
		resourceAttributes: { "k8s.pod.name": "web-7c41e9-x2kd9" },
	});
	const row = toRows([entry], new Map())[0];
	if (row === undefined) throw new Error("no row");
	expect(columnValue(row, "http.method")).toBe("GET");
	expect(columnValue(row, "body.status")).toBe("200");
	expect(columnValue(row, "replica")).toBe("x2kd9");
	expect(columnValue(row, "missing")).toBe("");
});
