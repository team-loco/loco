import { useState } from "react";

// A drawer's contents must outlive its `open` flag, or the panel empties (or unmounts) before
// its slide-out finishes. Returns the live value while open and the last open value after.
// Pass a value that keeps its identity between renders, such as an id: a fresh object each
// render would set state on every render.
export function useHeldWhileClosed<T>(value: T, open: boolean): T {
	const [held, setHeld] = useState(value);
	if (open && held !== value) setHeld(value);
	return open ? value : held;
}
