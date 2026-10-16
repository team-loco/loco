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
	const resuming = pending !== null && (key === pending.from || key === pending.to);
	return { key, on: playback.on, pending: resuming ? pending : null, paused: resuming ? playback.paused : null };
}

export function completeLogResume(playback: LogPlayback, pending: LogResume, success: boolean): LogPlayback {
	if (playback.pending !== pending) return playback;
	return { ...playback, pending: null, on: success, paused: success ? null : playback.paused };
}
