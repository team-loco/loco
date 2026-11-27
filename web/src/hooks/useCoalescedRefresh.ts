import { useEffect, useEffectEvent, useRef } from "react";

import { coalesced } from "@/lib/coalesce";

/** Cancels pending live refreshes when paused or when the subscription scope changes. */
export function useCoalescedRefresh(refresh: () => Promise<unknown>, gapMs: number, enabled: boolean, scope: string) {
	const handle = useEffectEvent(refresh);
	const timerRef = useRef<ReturnType<typeof coalesced> | null>(null);
	useEffect(() => {
		if (!enabled) return;
		const timer = coalesced(async () => await handle(), gapMs);
		timerRef.current = timer;
		return () => {
			timer.stop();
			timerRef.current = null;
		};
	}, [enabled, gapMs, scope]);
	return () => {
		timerRef.current?.poke();
	};
}
