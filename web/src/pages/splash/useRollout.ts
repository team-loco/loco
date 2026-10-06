import { useRef, useState } from "react";

import {
	initialRollout,
	MAX_REPLICAS,
	MIN_REPLICAS,
	nextTag,
	rollStep,
	scaleTo,
	settle,
	type PodPhase,
	type Rollout,
} from "./cluster";

export function useRollout() {
	const [rollout, setRollout] = useState(initialRollout);
	const current = useRef(rollout);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const podSeq = useRef(102);

	const commit = (next: Rollout) => {
		current.current = next;
		setRollout(next);
	};

	const schedule = (run: () => void, ms: number) => {
		if (timer.current !== null) clearTimeout(timer.current);
		timer.current = setTimeout(run, ms);
	};

	const makePod = (tag: string, phase: PodPhase) => {
		podSeq.current += 1;
		return { id: `p${podSeq.current}`, tag, phase };
	};

	const step = () => {
		const { rollout: next, delay } = rollStep(current.current, makePod);
		commit(next);
		if (delay !== null) schedule(step, delay);
	};

	const deploy = () => {
		const now = current.current;
		if (now.rolling) return;
		commit({ ...now, rolling: true, tag: nextTag(now.tag) });
		schedule(step, 300);
	};

	const scale = (target: number) => {
		const now = current.current;
		if (now.rolling || target < MIN_REPLICAS || target > MAX_REPLICAS) return;
		commit(scaleTo(now, target, makePod));
		schedule(() => {
			commit(settle(current.current));
		}, 900);
	};

	return { rollout, deploy, scale };
}
