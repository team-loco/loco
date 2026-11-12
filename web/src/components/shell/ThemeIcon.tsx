import "./ThemeIcon.css";

export function ThemeIcon({ mode }: { mode: "sun" | "moon" | "monitor" }) {
	return (
		<span className="flex size-[15px] items-center justify-center">
			<span className="theme-icon shrink-0 scale-[0.6]" data-mode={mode} />
		</span>
	);
}
