import { expect, test } from "bun:test";

import { parseDotEnv } from "./dotenv";

test("parseDotEnv skips comments, blank lines and lines that aren't KEY=value", () => {
	expect(parseDotEnv("# database\n\nDATABASE_URL=postgres://db/app\n   \nnot a pair\n1BAD=x\n=x")).toEqual([
		["DATABASE_URL", "postgres://db/app"],
	]);
});

test("parseDotEnv strips matching quotes and the export prefix", () => {
	expect(parseDotEnv(`export A="one two"\nB='a=b'\nC="half\nexport  D = plain `)).toEqual([
		["A", "one two"],
		["B", "a=b"],
		["C", '"half'],
		["D", "plain"],
	]);
});

test("parseDotEnv keeps the last value of a repeated key in first-seen order", () => {
	expect(parseDotEnv("A=1\r\nB=2\r\nA=3")).toEqual([
		["A", "3"],
		["B", "2"],
	]);
});
