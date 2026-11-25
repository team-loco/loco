import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { SecretSchema } from "@gen/loco/secret/v1/secret_pb";

import { importPreview, matchesSearch, secretNameProblem } from "./secrets";

describe("secretNameProblem", () => {
	test("asks for capital letters and refuses a name the environment has", () => {
		expect(secretNameProblem("", false)).toBe("");
		expect(secretNameProblem("lower", false)).toBe("format");
		expect(secretNameProblem("API_KEY", true)).toBe("taken");
		expect(secretNameProblem("API_KEY", false)).toBe("");
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
