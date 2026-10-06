import {
	initialRollout,
	rollStep,
	startRollout,
	type LogEvent,
	type PodPhase,
	type Rollout,
	type StepResult,
} from "./cluster";

export interface LogLine extends LogEvent {
	id: number;
	time: string;
}

export interface DemoSnapshot {
	rollout: Rollout;
	log: readonly LogLine[];
}

const FIRST_ROLLOUT_MS = 1600;
const ROLLOUT_INTERVAL_MS = 9000;
const LOG_LINES = 4;
const LOG_EPOCH = Date.UTC(2026, 9, 5, 12, 0, 0);
const LOG_SPACING_MS = 1700;

export class RolloutPlayer {
	private snapshot: DemoSnapshot = { rollout: initialRollout(), log: [] };
	private readonly listeners = new Set<() => void>();
	private stepTimer: ReturnType<typeof setTimeout> | null = null;
	private loopTimer: ReturnType<typeof setTimeout> | null = null;
	private podSeq = 102;
	private logSeq = 0;

	subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};

	getSnapshot = () => this.snapshot;

	observe = (el: HTMLElement | null) => {
		if (el === null) return;
		const observer = new IntersectionObserver(
			(entries) => {
				if (entries.some((e) => e.isIntersecting)) this.play();
				else this.pause();
			},
			{ threshold: 0.25 }
		);
		observer.observe(el);
		return () => {
			observer.disconnect();
			this.pause();
		};
	};

	private play() {
		if (this.loopTimer !== null) return;
		this.loopTimer = setTimeout(this.tick, FIRST_ROLLOUT_MS);
	}

	private pause() {
		if (this.loopTimer !== null) clearTimeout(this.loopTimer);
		this.loopTimer = null;
	}

	private tick = () => {
		const { rollout } = this.snapshot;
		if (!rollout.rolling) this.apply(startRollout(rollout));
		this.loopTimer = setTimeout(this.tick, ROLLOUT_INTERVAL_MS);
	};

	private step = () => {
		this.apply(rollStep(this.snapshot.rollout, this.makePod));
	};

	private makePod = (tag: string, phase: PodPhase) => {
		this.podSeq += 1;
		return { id: `p${this.podSeq}`, tag, phase };
	};

	private apply({ rollout, delay, event }: StepResult) {
		const log = event === null ? this.snapshot.log : [...this.snapshot.log, this.logLine(event)].slice(-LOG_LINES);
		this.snapshot = { rollout, log };
		for (const listener of this.listeners) listener();
		if (this.stepTimer !== null) clearTimeout(this.stepTimer);
		this.stepTimer = delay === null ? null : setTimeout(this.step, delay);
	}

	private logLine(event: LogEvent): LogLine {
		this.logSeq += 1;
		const time = new Date(LOG_EPOCH + this.logSeq * LOG_SPACING_MS).toISOString().slice(11, 19);
		return { ...event, id: this.logSeq, time };
	}
}
