import { expect, test } from "bun:test";
import { create, toBinary, toJson } from "@bufbuild/protobuf";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";
import { msToTimestamp } from "@/lib/time";
import { AROUND_MS } from "@/lib/obs-time";
import { logOffset, traceBounds, traceLogWindow } from "./trace-time";

test("fractional millisecond traces produce encodable query bounds", () => {
	const { start, end } = traceBounds({ start: 1_700_000_000_000_000_123n, duration: 427_600_123n });
	for (const ms of [start, end, start - AROUND_MS, end + AROUND_MS]) {
		const timestamp = msToTimestamp(ms);
		expect(Number.isInteger(timestamp.nanos)).toBe(true);
		expect(() => toBinary(TimestampSchema, timestamp)).not.toThrow();
		expect(() => toJson(TimestampSchema, timestamp)).not.toThrow();
	}
});

test("a log in the first trace millisecond keeps its positive nanosecond offset", () => {
	expect(
		logOffset({ start: 1_700_000_000_000_000_123n }, create(TimestampSchema, { seconds: 1_700_000_000n, nanos: 456 })),
	).toBe(333n);
});

test("trace log windows include the end of a twenty-minute trace", () => {
	const start = Date.now() - 6 * 60 * 60_000;
	const end = start + 20 * 60_000;
	const trace = { start: BigInt(start) * 1_000_000n, duration: BigInt(end - start) * 1_000_000n };
	expect(traceLogWindow(trace, Date.now())).toEqual({ kind: "absolute", from: start - AROUND_MS, to: end + AROUND_MS });
	expect(traceLogWindow(trace, end + 1000).to).toBe(end + 1000);
});
