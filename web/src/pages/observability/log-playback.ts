import type { LogRow } from "./rows";

export interface LogResume {
	from: string;
	to: string;
}

export interface LogPlayback {
	key: string;
	on: boolean;
	pending: LogResume | null;
	paused: { rows: LogRow[]; olderCursor: string; newerCursor: string } | null;
}

export function logPlaybackForKey(playback: LogPlayback, key: string): LogPlayback {
	if (playback.key === key) return playback;
	const pending = playback.pending;
	const resuming = pending !== null && playback.key === pending.from && key === pending.to;
	return { key, on: playback.on, pending: resuming ? pending : null, paused: resuming ? playback.paused : null };
}

export function completeLogResume(playback: LogPlayback, pending: LogResume, success: boolean): LogPlayback {
	if (playback.pending !== pending) return playback;
	return { ...playback, pending: null, on: success, paused: success ? null : playback.paused };
}

export function cancelLogResume(playback: LogPlayback): LogPlayback {
	return playback.pending === null ? playback : { ...playback, pending: null };
}

const RATE_WINDOW_MS = 10_000;

/** Lines per second that arrived in the last ten seconds, or since the tail started if that is shorter. */
export function tailRate(arrivals: number[], startedAt: number | null, nowMs: number): number {
	const seconds = Math.min(RATE_WINDOW_MS, Math.max(1000, nowMs - (startedAt ?? nowMs))) / 1000;
	return arrivals.filter((at) => at > nowMs - RATE_WINDOW_MS).length / seconds;
}
