import { RotateCwIcon } from "lucide-react";
import { toast } from "sonner";

// How often an open tab asks whether a newer dashboard build is being served.
const CHECK_INTERVAL_MS = 5 * 60_000;
const TOAST_ID = "loco-update";

async function servedVersion(): Promise<string | null> {
	try {
		const response = await fetch("/version.json", { cache: "no-store" });
		if (!response.ok) return null;
		const { version } = (await response.json()) as { version: string };
		return version;
	} catch {
		// Offline or mid-deploy: the next check tries again.
		return null;
	}
}

export function watchForUpdates() {
	const running = document.querySelector<HTMLMetaElement>('meta[name="loco-version"]')?.content;
	// The dev server stamps no version, so there is nothing to compare against.
	if (running === undefined) return;
	let notified = running;
	const check = async () => {
		if (document.visibilityState !== "visible") return;
		const served = await servedVersion();
		if (served === null || served === notified) return;
		notified = served;
		if (served === running) {
			toast.dismiss(TOAST_ID);
			return;
		}
		toast("Loco has been updated", {
			id: TOAST_ID,
			duration: Infinity,
			icon: (
				<span className="h-[13px] w-2.5 bg-info" style={{ mask: "url(/favicon.svg) center / contain no-repeat" }} />
			),
			action: {
				label: (
					<>
						<RotateCwIcon />
						Reload
					</>
				),
				onClick: () => {
					window.location.reload();
				},
			},
			classNames: { closeButton: "opacity-100!" },
		});
	};
	setInterval(() => void check(), CHECK_INTERVAL_MS);
	document.addEventListener("visibilitychange", () => void check());
}
