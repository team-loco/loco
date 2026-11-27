import { create, fromJsonString, toJsonString, type MessageInitShape } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { EventService, StreamEventsRequestSchema } from "@gen/loco/event/v1/event_pb";
import { useEffect, useEffectEvent, useState } from "react";

import { followEvents } from "@/lib/follow-events";

/** Resumes after history’s publication boundary and refreshes on connection and event batches. */
export function useEventSubscription(
	request: MessageInitShape<typeof StreamEventsRequestSchema>,
	enabled: boolean,
	initialCursor: string,
	onRefresh: () => void,
) {
	const transport = useTransport();
	const input = toJsonString(StreamEventsRequestSchema, create(StreamEventsRequestSchema, request));
	const [failure, setFailure] = useState<{ input: string; error: unknown } | null>(null);
	const [attempt, setAttempt] = useState(0);
	const handle = useEffectEvent(onRefresh);
	const [position, setPosition] = useState({ input, cursor: "" });
	const currentCursor = (position.input === input ? position.cursor : "") || initialCursor;
	const ready = enabled && currentCursor !== "";
	const resumeCursor = useEffectEvent(() => currentCursor);
	useEffect(() => {
		if (!ready) return;
		const abort = new AbortController();
		const client = createClient(EventService, transport);
		const scoped = fromJsonString(StreamEventsRequestSchema, input);
		void followEvents(
			(cursor, signal) => {
				const from = cursor || resumeCursor();
				return client.streamEvents(
					{ ...scoped, cursor: from },
					{
						signal,
						onHeader: () => {
							if (abort.signal.aborted) return;
							setPosition({ input, cursor: from });
							setFailure(null);
							handle();
						},
					},
				);
			},
			(_events, cursor) => {
				setPosition({ input, cursor });
				handle();
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
	}, [ready, input, transport, attempt]);
	return {
		error: ready && failure?.input === input ? failure.error : null,
		retry: () => {
			setAttempt((value) => value + 1);
		},
	};
}
