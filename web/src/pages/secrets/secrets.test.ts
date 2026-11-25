import { create } from "@bufbuild/protobuf";
import { textOf } from "@/lib/node-text";
import { describe, expect, test } from "bun:test";
import { SecretSchema } from "@gen/loco/secret/v1/secret_pb";

import { deleteBlocked, importPreview, matchesSearch, secretHint, secretNameProblem } from "./secrets";

describe("secretNameProblem", () => {
	test("asks for capital letters and refuses a name the environment has", () => {
		expect(secretNameProblem("", false, "production")).toBe("");
		expect(textOf(secretNameProblem("lower", false, "production"))).toBe(
			"Use capital letters, digits and underscores, not starting with a digit.",
		);
		expect(textOf(secretNameProblem("API_KEY", true, "production"))).toBe("API_KEY already exists in production.");
		expect(secretNameProblem("API_KEY", false, "production")).toBe("");
	});
});

describe("secretHint", () => {
	test("shows the reference for a new secret and who gets an updated value", () => {
		expect(textOf(secretHint("", [], false))).toBe("Refer to it from a resource’s variables as ${{ secrets.NAME }}.");
		expect(textOf(secretHint("API_KEY", [], true))).toBe("No resource refers to it yet.");
		expect(textOf(secretHint("API_KEY", ["api"], true))).toBe("api gets the new value on its next deploy or restart.");
		expect(textOf(secretHint("API_KEY", ["api", "web"], true))).toBe(
			"api and web get the new value on their next deploy or restart.",
		);
	});
});

describe("deleteBlocked", () => {
	test("names the resources that keep a secret from being deleted", () => {
		expect(textOf(deleteBlocked(["api", "web"]))).toBe("Read by api and web. Remove the reference and redeploy first.");
	});
});

describe("importPreview", () => {
	test("counts the names found and those that replace a secret", () => {
		const preview = importPreview("# comment\nexport API_KEY=a\nNEW=b\n", new Set(["API_KEY"]));
		expect(preview.values).toEqual({ API_KEY: "a", NEW: "b" });
		expect(preview.summary).toBe("2 found, 1 replace existing");
		expect(importPreview("", new Set()).summary).toBe("Nothing found yet");
		expect(importPreview("lower=1", new Set()).summary).toBe("Not valid secret names: lower");
	});

	test("notes the skipped lines, repeated names and replaced secrets", () => {
		const preview = importPreview("A=1\noops\nA=2\nB=3", new Set(["B"]));
		expect(preview.values).toEqual({ A: "2", B: "3" });
		expect(preview.notes).toEqual([
			"Skipped line 2 (not KEY=value).",
			"A appears more than once; the last value is used.",
			"Replaces B.",
		]);
	});
});

describe("matchesSearch", () => {
	test("matches the name or a resource that uses the secret", () => {
		const secret = create(SecretSchema, { name: "DATABASE_URL", usedBy: [{ resourceSlug: "worker" }] });
		expect(matchesSearch(secret, "database")).toBe(true);
		expect(matchesSearch(secret, "work")).toBe(true);
		expect(matchesSearch(secret, "web")).toBe(false);
	});
});
