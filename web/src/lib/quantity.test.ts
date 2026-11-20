import { describe, expect, test } from "bun:test";

import { formatCpu, formatMemory } from "./quantity";

describe("quantity formatting", () => {
	test("non-zero usage never renders as zero", () => {
		expect(formatCpu(0.4)).toBe("<0.001 vCPU");
		expect(formatCpu(1)).toBe("0.001 vCPU");
		expect(formatCpu(0)).toBe("0 vCPU");
		expect(formatMemory(0.2)).toBe("<1 Mi");
		expect(formatMemory(0)).toBe("0 Mi");
	});
});
