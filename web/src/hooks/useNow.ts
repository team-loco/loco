import { useEffect, useState } from "react";

// holds the clock in state so render stays pure; calling Date during render
// makes the component impure and React Compiler then skips memoizing it
export function useNow(intervalMs?: number): Date {
	const [now, setNow] = useState(() => new Date());

	useEffect(() => {
		if (intervalMs === undefined) return;
		const id = setInterval(() => {
			setNow(new Date());
		}, intervalMs);
		return () => {
			clearInterval(id);
		};
	}, [intervalMs]);

	return now;
}
