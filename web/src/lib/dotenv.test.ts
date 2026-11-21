import { expect, test } from "bun:test";

import { dotEnvNotes, parseDotEnv } from "./dotenv";

test("parseDotEnv skips comments, blank lines and lines that aren't KEY=value", () => {
	expect(parseDotEnv("# database\n\nDATABASE_URL=postgres://db/app\n   \nnot a pair\n1BAD=x\n=x").pairs).toEqual([
		["DATABASE_URL", "postgres://db/app"],
	]);
});

test("parseDotEnv numbers the lines it skips, leaving out blanks and comments", () => {
	expect(parseDotEnv("# c\nA=1\n\nnot a pair\n1BAD=x\r\nB=2\n=x").skipped).toEqual([4, 5, 7]);
});

test("parseDotEnv strips matching quotes and the export prefix", () => {
	expect(parseDotEnv(`export A="one two"\nB='a=b'\nC="half\nexport  D = plain `).pairs).toEqual([
		["A", "one two"],
		["B", "a=b"],
		["C", '"half'],
		["D", "plain"],
	]);
});

test("parseDotEnv keeps the last value of a repeated key in first-seen order and names the key", () => {
	const parsed = parseDotEnv("A=1\r\nB=2\r\nA=3\nA=4\nB=5");
	expect(parsed.pairs).toEqual([
		["A", "4"],
		["B", "5"],
	]);
	expect(parsed.repeated).toEqual(["A", "B"]);
});

test("dotEnvNotes reports skipped lines, repeated keys and replaced keys", () => {
	const parsed = parseDotEnv("A=1\nbad\nA=2\nB=3\nC=4");
	expect(dotEnvNotes(parsed, new Set(["B", "Z"]))).toEqual([
		"Skipped line 2 (not KEY=value).",
		"A appears more than once; the last value is used.",
		"Replaces B.",
	]);
});

test("dotEnvNotes is empty for a clean import", () => {
	expect(dotEnvNotes(parseDotEnv("A=1\nB=2"), new Set())).toEqual([]);
});
