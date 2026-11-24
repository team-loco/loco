import { describe, expect, test } from "bun:test";

import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "./quantity";

describe("quantity formatting", () => {
	test("non-zero usage never renders as zero", () => {
		expect(formatCpu(0.4)).toBe("<0.001 vCPU");
		expect(formatCpu(1)).toBe("0.001 vCPU");
		expect(formatCpu(0)).toBe("0 vCPU");
		expect(formatMemory(0.2)).toBe("<1 Mi");
		expect(formatMemory(0)).toBe("0 Mi");
	});
});

describe("parseCpuMilli", () => {
	test.each([
		["250m", 250],
		["0.5", 500],
		[".5", 500],
		["2", 2000],
		["1e3", 1_000_000],
		["1500m", 1500],
		["", 0],
		["abc", 0],
	])("%s", (value, want) => {
		expect(parseCpuMilli(value)).toBeCloseTo(want);
	});
});

describe("parseMemMi", () => {
	test.each([
		["256Mi", 256],
		["1Gi", 1024],
		["1Ti", 1024 * 1024],
		["1Pi", 1024 ** 3],
		["1Ei", 1024 ** 4],
		["1.5Gi", 1536],
		["1e9", 1e9 / 1_048_576],
		["1G", 1e9 / 1_048_576],
		["1T", 1e12 / 1_048_576],
		["1P", 1e15 / 1_048_576],
		["1E", 1e18 / 1_048_576],
		["1048576", 1],
		["", 0],
		["1Zi", 0],
	])("%s", (value, want) => {
		expect(parseMemMi(value)).toBeCloseTo(want, 3);
	});
});
