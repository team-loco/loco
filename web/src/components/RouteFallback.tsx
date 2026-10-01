import Loader from "@/assets/loader.svg?react";

export function RouteFallback() {
	return (
		<div
			role="status"
			aria-live="polite"
			className="flex min-h-[60vh] w-full items-center justify-center"
		>
			<Loader className="h-6 w-6" />
			<span className="sr-only">Loading…</span>
		</div>
	);
}
