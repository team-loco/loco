import { useState } from "react";

/** The latest query error, kept while a later fetch runs and cleared once fetching ends without one. */
export function useHeldError<E>(error: E | null, fetching: boolean): E | null {
	const [held, setHeld] = useState<E | null>(null);
	if (error !== null && error !== held) setHeld(error);
	if (error === null && !fetching && held !== null) setHeld(null);
	return error ?? (fetching ? held : null);
}
