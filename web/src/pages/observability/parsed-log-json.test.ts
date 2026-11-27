import { expect, test } from "bun:test";
import { parsedLogJson } from "./parsed-log-json";

function log(attributes: Record<string, string>): string {
	return JSON.stringify({ message: "handled request", resource_attributes: {}, log_attributes: attributes });
}

test("offers a parsed preview only for valid object or array attributes", () => {
	for (const value of ["hello", "123", "true", "null", '"text"', '{"broken":', "[1,]"]) {
		expect(parsedLogJson(log({ request: value }))).toBeNull();
	}
	const original = log({ request: '{"entity":null,"path":"/ListOrgs"}', items: "[true,{},[]]", text: "hello" });
	const preview = parsedLogJson(original);
	expect(JSON.parse(preview!)).toEqual({
		message: "handled request",
		resource_attributes: {},
		log_attributes: { request: { entity: null, path: "/ListOrgs" }, items: [true, {}, []], text: "hello" },
	});
	expect((JSON.parse(original) as { log_attributes: { request: string } }).log_attributes.request).toBe(
		'{"entity":null,"path":"/ListOrgs"}',
	);
});

test("keeps exact numeric tokens and does not repeatedly interpret nested strings", () => {
	const preview = parsedLogJson(log({ request: '{"id":9007199254740993,"zero":-0,"text":"{\\"id\\":1}"}' }));
	expect(preview).toContain('"id": 9007199254740993');
	expect(preview).toContain('"zero": -0');
	expect(preview).toContain('"text": "{\\"id\\":1}"');
});
