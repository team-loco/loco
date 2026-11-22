import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { EventSchema, type Event } from "@gen/loco/event/v1/event_pb";

import { followEvents } from "./useDeployEvents";

const event = (id: string) => create(EventSchema, { id, type: "resource.degraded" });

test("a dropped stream or expired session resumes from the last cursor, so no event arrives twice", async () => {
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
			abort.abort();
		},
		(events: Event[]) => {
			seen.push(...events.map((e) => e.id));
		},
		abort.signal,
		0,
	);
	expect(cursors).toEqual(["", "c1", "c2"]);
	expect(seen).toEqual(["e1", "e2"]);
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
		0,
	);
	expect(opened).toBe(1);
});
