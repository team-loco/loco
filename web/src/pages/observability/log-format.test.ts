import { expect, test } from "bun:test";
import { Fragment, createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { bodyParts } from "./format";

function renderBody(body: string, words: string[] = []): string {
	const parts = bodyParts(body, words);
	return renderToStaticMarkup(createElement(Fragment, null, parts));
}

test("terminal styles do not obscure the log message or its search highlight", () => {
	const body =
		'\u001b[2m2026-10-16T07:58:42Z\u001b[0m \u001b[32mINFO\u001b[0m Track\u001b[3ming ID Created\u001b[0m tracking_id=\u001b[38;2;90;20;10m"order-123"\u001b[0m';
	const html = renderBody(body, ["Tracking"]);
	expect(html).not.toContain("\u001b");
	expect(html).not.toContain("[32m");
	expect(html).toContain(">Tracking</mark>");
	expect(html).toContain(" ID Created");
	expect(html).toContain("order-123");
});

test("log rendering preserves literal bracket text, newlines and HTML escaping", () => {
	const html = renderBody("literal [32m stays\n日本語 <script>message</script>");
	expect(html).toContain("literal [32m stays\n日本語 &lt;script&gt;message&lt;/script&gt;");
	expect(renderBody("")).toBe("");
});

test("colon RGB styles, C1 styles and a default reset leave readable text", () => {
	const html = renderBody("\u001b[38:2::90:20:10mcolor\u001b[m \u009b1mbold\u009b0m");
	expect(html).toBe("color bold");
});
