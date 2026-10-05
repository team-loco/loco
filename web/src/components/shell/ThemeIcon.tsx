import "./ThemeIcon.css";

export function ThemeIcon({ mode }: { mode: "sun" | "moon" }) {
	return (
		<span className="flex size-[15px] items-center justify-center">
			<span className="theme-icon shrink-0 scale-[0.6]" data-mode={mode} />
		</span>
	);
}

export async function playThemeSound(toDark: boolean) {
	const audio = new Audio(toDark ? "/darkMode.wav" : "/lightMode.wav");
	audio.volume = 0.9;
	await audio.play().catch(() => undefined);
}
