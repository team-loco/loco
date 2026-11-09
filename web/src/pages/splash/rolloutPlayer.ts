import { REST_FRAME } from "./deploy";

const TICK_MS = 450;
const HOLD_FRAMES = 14;
const LOOP_FRAMES = REST_FRAME + HOLD_FRAMES;
const VISIBLE_SHARE = 0.25;

export class RolloutPlayer {
	private frame: number;
	private readonly still: boolean;
	private readonly listeners = new Set<() => void>();
	private timer: ReturnType<typeof setTimeout> | null = null;

	constructor(still: boolean) {
		this.still = still;
		this.frame = still ? REST_FRAME : 0;
	}

	subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};

	getSnapshot = () => this.frame;

	observe = (el: HTMLElement | null) => {
		if (el === null || this.still) return;
		const observer = new IntersectionObserver(
			(entries) => {
				if (entries.some((e) => e.isIntersecting)) this.play();
				else this.pause();
			},
			{ threshold: VISIBLE_SHARE },
		);
		observer.observe(el);
		return () => {
			observer.disconnect();
			this.pause();
		};
	};

	private play() {
		if (this.timer !== null) return;
		this.timer = setTimeout(this.advance, TICK_MS);
	}

	private pause() {
		if (this.timer !== null) clearTimeout(this.timer);
		this.timer = null;
	}

	private advance = () => {
		this.frame = (this.frame + 1) % LOOP_FRAMES;
		for (const listener of this.listeners) listener();
		this.timer = setTimeout(this.advance, TICK_MS);
	};
}
