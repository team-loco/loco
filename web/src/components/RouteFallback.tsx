import { Loader2 } from "lucide-react";

export function RouteFallback() {
	return (
		<div role="status" aria-live="polite" className="flex min-h-[60vh] w-full items-center justify-center">
			<Loader2 className="size-5 animate-spin text-fg3" />
			<span className="sr-only">Loading…</span>
		</div>
	);
}
