import { create, fromJsonString, toJsonString, type MessageInitShape } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { EventService, StreamEventsRequestSchema, type Event } from "@gen/loco/event/v1/event_pb";
import { useEffect, useEffectEvent, useState } from "react";

import { followEvents } from "@/lib/follow-events";

/** Owns the live subscription; history remains in the caller's paginated query. */
export function useEventSubscription(
	request: MessageInitShape<typeof StreamEventsRequestSchema>,
	enabled: boolean,
	onEvents: (events: Event[]) => void,
) {
	const transport = useTransport();
	const input = toJsonString(StreamEventsRequestSchema, create(StreamEventsRequestSchema, request));
	const [failure, setFailure] = useState<{ input: string; error: unknown } | null>(null);
	const [attempt, setAttempt] = useState(0);
	const handle = useEffectEvent(onEvents);
	const [position, setPosition] = useState({ input, cursor: "" });
	const resumeCursor = useEffectEvent(() => (position.input === input ? position.cursor : ""));
	useEffect(() => {
		if (!enabled) return;
		const abort = new AbortController();
		const client = createClient(EventService, transport);
		const scoped = fromJsonString(StreamEventsRequestSchema, input);
		void followEvents(
			(cursor, signal) =>
				client.streamEvents(
					{ ...scoped, cursor: cursor || resumeCursor() },
					{
						signal,
						onHeader: () => {
							if (abort.signal.aborted) return;
							setFailure(null);
						},
					},
				),
			(events, cursor) => {
				setPosition({ input, cursor });
				handle(events);
			},
			abort.signal,
			{
				retryMs: 1_000,
				maxRetries: 3,
				onError: (error) => {
					setFailure({ input, error });
				},
			},
		);
		return () => {
			abort.abort();
		};
	}, [enabled, input, transport, attempt]);
	return {
		cursor: position.input === input ? position.cursor : "",
		error: enabled && failure?.input === input ? failure.error : null,
		retry: () => {
			setAttempt((value) => value + 1);
		},
	};
}
