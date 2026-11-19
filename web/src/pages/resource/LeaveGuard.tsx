import { useEffect } from "react";
import { useBlocker, type Location } from "react-router";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";

function place(location: Location): string {
	return `${location.pathname}?env=${new URLSearchParams(location.search).get("env") ?? ""}`;
}

/** Asks before navigating away from, or reloading, a resource page with unsaved settings. */
export function LeaveGuard({ dirty, resourceName }: { dirty: boolean; resourceName: string }) {
	const blocker = useBlocker(
		({ currentLocation, nextLocation }) => dirty && place(currentLocation) !== place(nextLocation),
	);
	useEffect(() => {
		if (!dirty) return;
		const warn = (event: BeforeUnloadEvent) => {
			event.preventDefault();
		};
		window.addEventListener("beforeunload", warn);
		return () => {
			window.removeEventListener("beforeunload", warn);
		};
	}, [dirty]);
	return (
		<ConfirmDialog
			open={blocker.state === "blocked"}
			title="Discard unsaved changes?"
			description={`The changes to the settings of ${resourceName} are not saved. Leaving this page discards them.`}
			confirmLabel="Discard changes"
			cancelLabel="Keep editing"
			onConfirm={() => {
				blocker.proceed?.();
			}}
			onClose={() => {
				blocker.reset?.();
			}}
		/>
	);
}
