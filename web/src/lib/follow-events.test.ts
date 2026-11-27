import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { EventSchema, type Event } from "@gen/loco/event/v1/event_pb";

import { followEvents } from "@/lib/follow-events";

const event = (id: string) => create(EventSchema, { id, type: "resource.degraded" });

test("a dropped stream, expired session or changed access resumes from the last cursor", async () => {
	const abort = new AbortController();
	const cursors: string[] = [];
	const seen: string[] = [];
	await followEvents(
		async function* (cursor) {
			cursors.push(cursor);
			await Promise.resolve();
			if (cursors.length === 1) {
				yield { events: [event("e1")], cursor: "c1" };
				throw new ConnectError("connection reset", Code.Unavailable);
			}
			if (cursors.length === 2) {
				yield { events: [event("e2")], cursor: "c2" };
				throw new ConnectError("session expired", Code.Unauthenticated);
			}
			if (cursors.length === 3) {
				yield { events: [event("e3")], cursor: "c3" };
				throw new ConnectError("subscription access changed", Code.Aborted);
			}
			abort.abort();
		},
		(events: Event[]) => {
			seen.push(...events.map((e) => e.id));
		},
		abort.signal,
		{ retryMs: 0 },
	);
	expect(cursors).toEqual(["", "c1", "c2", "c3"]);
	expect(seen).toEqual(["e1", "e2", "e3"]);
});

test("a refused stream stops", async () => {
	let opened = 0;
	await followEvents(
		() => ({
			[Symbol.asyncIterator]: () => ({
				next: async () => {
					opened += 1;
					return await Promise.reject(new ConnectError("denied", Code.PermissionDenied));
				},
			}),
		}),
		() => undefined,
		new AbortController().signal,
		{ retryMs: 0 },
	);
	expect(opened).toBe(1);
});

test("repeated transient failures stop after bounded retries and report the failure", async () => {
	let opened = 0;
	const errors: unknown[] = [];
	const failure = new ConnectError("server unavailable", Code.Unavailable);
	await followEvents(
		async function* () {
			opened += 1;
			yield await Promise.reject(failure);
		},
		() => undefined,
		new AbortController().signal,
		{
			retryMs: 0,
			maxRetries: 3,
			onError: (error) => {
				errors.push(error);
			},
		},
	);
	expect(opened).toBe(4);
	expect(errors).toEqual([failure, failure, failure, failure]);
});

test("aborting a retry delay stops reconnecting without reporting cancellation", async () => {
	const abort = new AbortController();
	let opened = 0;
	const errors: unknown[] = [];
	await followEvents(
		async function* () {
			opened += 1;
			yield await Promise.reject(new ConnectError("offline", Code.Unavailable));
		},
		() => undefined,
		abort.signal,
		{
			retryMs: 60_000,
			onError: (error) => {
				errors.push(error);
				abort.abort();
			},
		},
	);
	expect(opened).toBe(1);
	expect(errors).toHaveLength(1);
});
