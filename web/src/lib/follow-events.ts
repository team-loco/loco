import { Code, ConnectError } from "@connectrpc/connect";
import type { Event } from "@gen/loco/event/v1/event_pb";

const RETRY_MS = 1_000;
const MAX_RETRIES = 3;

/** Reconnects after transient failures from the last delivered cursor. */
export async function followEvents(
	open: (cursor: string, signal: AbortSignal) => AsyncIterable<{ events: Event[]; cursor: string }>,
	onEvents: (events: Event[], cursor: string) => void,
	signal: AbortSignal,
	retryMs = RETRY_MS,
	onError: (error: unknown) => void = () => undefined,
): Promise<void> {
	let cursor = "";
	let failures = 0;
	const aborted = () => signal.aborted;
	while (!aborted()) {
		try {
			for await (const message of open(cursor, signal)) {
				if (signal.aborted) return;
				if (message.events.length > 0) onEvents(message.events, message.cursor);
				if (message.cursor !== "") cursor = message.cursor;
				failures = 0;
			}
			if (signal.aborted) return;
			throw new ConnectError("Event stream closed", Code.Unavailable);
		} catch (error) {
			if (signal.aborted) return;
			onError(error);
			if (terminal(error) || failures === MAX_RETRIES) return;
		}
		await new Promise<void>((resolve) => {
			const done = () => {
				clearTimeout(timer);
				signal.removeEventListener("abort", done);
				resolve();
			};
			const timer = setTimeout(done, retryMs * 2 ** failures);
			signal.addEventListener("abort", done, { once: true });
			if (signal.aborted) done();
		});
		failures += 1;
	}
}

function terminal(error: unknown): boolean {
	if (!(error instanceof ConnectError)) return false;
	return [Code.PermissionDenied, Code.InvalidArgument, Code.NotFound, Code.Unimplemented].includes(error.code);
}
