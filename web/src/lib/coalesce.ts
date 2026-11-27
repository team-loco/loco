/** Runs refresh gapMs after a poke, one run at a time; pokes during a run start one more run gapMs after it settles. */
export function coalesced(refresh: () => Promise<unknown>, gapMs: number) {
	let timer: ReturnType<typeof setTimeout> | undefined;
	let running = false;
	let again = false;
	let stopped = false;
	const schedule = () => {
		timer = setTimeout(() => {
			timer = undefined;
			running = true;
			void refresh().finally(() => {
				running = false;
				if (again && !stopped) {
					again = false;
					schedule();
				}
			});
		}, gapMs);
	};
	return {
		poke: () => {
			if (running) again = true;
			else if (timer === undefined) schedule();
		},
		stop: () => {
			stopped = true;
			clearTimeout(timer);
		},
	};
}
