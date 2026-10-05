import { createContext, use, useEffect, useId, useRef, useState, type ReactNode } from "react";

import { LocoLogo } from "@/components/design/LocoLogo";
import { cn } from "@/lib/utils";

const SHOW_DELAY_MS = 120;
const GRACE_MS = 150;
const FADE_MS = 200;

type Phase = "hidden" | "visible" | "finishing" | "fading";

interface AppLoaderApi {
	set: (id: string, message: string | undefined) => void;
	remove: (id: string) => void;
}

const AppLoaderContext = createContext<AppLoaderApi | null>(null);

export function AppLoaderProvider({ children }: { children: ReactNode }) {
	const [entries, setEntries] = useState<ReadonlyMap<string, string | undefined>>(() => new Map());

	const api: AppLoaderApi = {
		set: (id, message) => {
			setEntries((prev) => {
				if (prev.has(id) && prev.get(id) === message) return prev;
				const next = new Map(prev);
				next.set(id, message);
				return next;
			});
		},
		remove: (id) => {
			setEntries((prev) => {
				if (!prev.has(id)) return prev;
				const next = new Map(prev);
				next.delete(id);
				return next;
			});
		},
	};

	let message: string | undefined;
	for (const value of entries.values()) {
		if (value !== undefined) message = value;
	}

	return (
		<AppLoaderContext value={api}>
			{children}
			<AppLoaderOverlay active={entries.size > 0} message={message} />
		</AppLoaderContext>
	);
}

export function useAppLoader(active: boolean, message?: string) {
	const ctx = use(AppLoaderContext);
	const id = useId();
	useEffect(() => {
		if (!ctx || !active) return;
		ctx.set(id, message);
	}, [ctx, id, active, message]);
	useEffect(() => {
		if (!ctx || !active) return;
		return () => {
			ctx.remove(id);
		};
	}, [ctx, id, active]);
}

export function AppLoading({ message }: { message?: string | undefined }) {
	useAppLoader(true, message);
	return null;
}

function AppLoaderOverlay({ active, message }: { active: boolean; message: string | undefined }) {
	const [phase, setPhase] = useState<Phase>("hidden");
	const fadeTimer = useRef<number | undefined>(undefined);
	const reactivated = active && (phase === "finishing" || phase === "fading");
	const shown: Phase = reactivated ? "visible" : phase;

	useEffect(() => {
		if (active) {
			window.clearTimeout(fadeTimer.current);
			if (phase === "hidden") {
				const t = window.setTimeout(() => {
					setPhase("visible");
				}, SHOW_DELAY_MS);
				return () => {
					window.clearTimeout(t);
				};
			}
			if (reactivated) {
				const t = window.setTimeout(() => {
					setPhase("visible");
				}, 0);
				return () => {
					window.clearTimeout(t);
				};
			}
			return;
		}
		if (phase === "visible") {
			const t = window.setTimeout(() => {
				setPhase("finishing");
			}, GRACE_MS);
			return () => {
				window.clearTimeout(t);
			};
		}
		return;
	}, [active, phase, reactivated]);

	if (shown === "hidden") return null;

	const finishFade = () => {
		window.clearTimeout(fadeTimer.current);
		setPhase((p) => (p === "fading" ? "hidden" : p));
	};

	return (
		<div
			role="status"
			aria-live="polite"
			aria-busy={shown === "visible"}
			className={cn(
				"fixed inset-0 z-[400] flex flex-col items-center justify-center gap-4 bg-background transition-opacity ease-out",
				shown === "fading" && "opacity-0",
			)}
			style={{ transitionDuration: `${FADE_MS}ms` }}
			onTransitionEnd={(e) => {
				if (e.target === e.currentTarget) finishFade();
			}}
		>
			<LocoLogo
				motion="loop"
				speed={1.6}
				finishing={shown === "finishing" || shown === "fading"}
				onFinished={() => {
					setPhase((p) => (p === "finishing" ? "fading" : p));
					window.clearTimeout(fadeTimer.current);
					fadeTimer.current = window.setTimeout(finishFade, FADE_MS + 100);
				}}
				className="w-28"
				title="Loading"
			/>
			<span className={cn("h-5 text-fg3 transition-opacity", message === undefined && "opacity-0")}>
				{message ?? ""}
			</span>
		</div>
	);
}
