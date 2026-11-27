import { afterEach, expect, jest, test } from "bun:test";

import { coalesced } from "./coalesce";

afterEach(() => {
	jest.useRealTimers();
});

const settle = async () => {
	for (let i = 0; i < 5; i++) await Promise.resolve();
};

test("a burst of pokes runs one refresh at a time, gapMs apart, and once after the last poke", async () => {
	jest.useFakeTimers();
	let clock = 0;
	let inFlight = 0;
	let maxInFlight = 0;
	const starts: number[] = [];
	const refresh = coalesced(async () => {
		starts.push(clock);
		inFlight++;
		maxInFlight = Math.max(maxInFlight, inFlight);
		await new Promise((resolve) => setTimeout(resolve, 300));
		inFlight--;
	}, 2000);
	for (clock = 0; clock < 9000;) {
		if (clock < 4400) refresh.poke();
		clock += 50;
		jest.advanceTimersByTime(50);
		await settle();
	}
	expect(starts).toEqual([2000, 4300, 6600]);
	expect(maxInFlight).toBe(1);
});

test("stop cancels a pending refresh", async () => {
	jest.useFakeTimers();
	let runs = 0;
	const refresh = coalesced(async () => {
		runs++;
		await Promise.resolve();
	}, 2000);
	refresh.poke();
	refresh.stop();
	jest.advanceTimersByTime(5000);
	await settle();
	expect(runs).toBe(0);
});
