import { expect, test } from "bun:test";
import { createLoader, createSerializer } from "nuqs";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { columnValue, DEFAULT_COLUMNS, columnsParser, MIN_COLUMN_WIDTH, widthsParser } from "./columns";
import { toRows } from "./rows";

const PARSERS = { cols: columnsParser, widths: widthsParser };
const load = createLoader(PARSERS);
const serialize = createSerializer(PARSERS);

test("default columns stay out of the URL and custom ones round-trip", () => {
	expect(serialize({ cols: DEFAULT_COLUMNS })).toBe("");
	expect(load("").cols).toEqual(DEFAULT_COLUMNS);
	const custom = ["level", "replica", "http.method"];
	expect(load(serialize({ cols: custom })).cols).toEqual(custom);
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

test("request columns read the common attribute names", () => {
	const entry = create(LogEntrySchema, {
		body: "GET /cart 200",
		logAttributes: { "http.request.method": "GET", "http.route": "/cart", status: "200", duration_ms: "12" },
	});
	const row = toRows([entry], new Map())[0];
	if (row === undefined) throw new Error("no row");
	expect(["method", "path", "status", "duration"].map((c) => columnValue(row, c))).toEqual([
		"GET",
		"/cart",
		"200",
		"12",
	]);
});

test("column widths round-trip and stay within bounds", () => {
	const widths = { resource: 200, "http.route": 320 };
	expect(load(serialize({ widths })).widths).toEqual(widths);
	expect(load("?widths=level:1,bad,path:x").widths).toEqual({ level: MIN_COLUMN_WIDTH });
	expect(serialize({ widths: {} })).toBe("");
});
