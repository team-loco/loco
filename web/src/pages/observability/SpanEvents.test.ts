import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { TraceSpanEventSchema } from "@gen/loco/observability/v1/observability_pb";

import { SpanEvents } from "./SpanEvents";

describe("span events", () => {
	test("renders emission order, exception details and multiline stack text", () => {
		const html = renderToStaticMarkup(
			createElement(SpanEvents, {
				events: [
					create(TraceSpanEventSchema, {
						name: "retry",
						attributes: { attempt: "2" },
					}),
					create(TraceSpanEventSchema, {
						name: "exception",
						attributes: {
							"exception.type": "TimeoutError",
							"exception.message": "<timed out>",
							"exception.stacktrace": "TimeoutError\n  at request (client.ts:42)",
						},
					}),
				],
			}),
		);
		expect(html.indexOf("retry")).toBeLessThan(html.indexOf("exception"));
		expect(html).toContain("&lt;timed out&gt;");
		expect(html).toContain("TimeoutError\n  at request (client.ts:42)");
		expect(html).toContain("Copy stack trace");
		expect(html).not.toContain("exception.stacktrace</dt>");
	});
	test("distinguishes a span without recorded events", () => {
		expect(renderToStaticMarkup(createElement(SpanEvents, { events: [] }))).toContain(
			"No events recorded for this span.",
		);
	});
});
