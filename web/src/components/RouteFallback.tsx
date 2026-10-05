import { LocoLogo } from "@/components/design/LocoLogo";

export function RouteFallback() {
	return (
		<div role="status" aria-live="polite" className="flex min-h-[60vh] w-full items-center justify-center">
			<LocoLogo motion="loop" speed={1.6} className="w-28" title="Loading" />
		</div>
	);
}
