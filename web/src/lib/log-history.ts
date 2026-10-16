import { LogOrder } from "@gen/loco/observability/v1/observability_pb";

export interface LogBatch {
	cursor: string;
	order: LogOrder;
}

export interface LogHistory {
	older: LogBatch[];
	current: LogBatch;
	newer: LogBatch[];
}

export function createLogHistory(): LogHistory {
	return { older: [], current: { cursor: "", order: LogOrder.NEWEST_FIRST }, newer: [] };
}

export function moveLogHistory(history: LogHistory, direction: "older" | "newer", cursor: string): LogHistory {
	const cached = history[direction].at(-1);
	if (cached === undefined && cursor === "") throw new Error("No log batch in this direction");
	const opposite = direction === "older" ? "newer" : "older";
	const current = cached ?? { cursor, order: direction === "older" ? LogOrder.NEWEST_FIRST : LogOrder.OLDEST_FIRST };
	return { ...history, current,
		[direction]: cached === undefined ? history[direction] : history[direction].slice(0, -1),
		[opposite]: [...history[opposite], history.current] };
}

export function logAnchor(value: string | null): number | null {
	const timestamp = Number(value);
	return Number.isSafeInteger(timestamp) && timestamp > 0 ? timestamp : null;
}
