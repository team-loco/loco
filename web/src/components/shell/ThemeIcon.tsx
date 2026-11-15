import "./ThemeIcon.css";

export function ThemeIcon({ mode, active }: { mode: "sun" | "moon" | "monitor"; active?: boolean }) {
	return (
		<span
			className="theme-icon-motion flex size-[15px] items-center justify-center"
			data-motion={active === true ? mode : undefined}
		>
			<span className="theme-icon shrink-0 scale-[0.6]" data-mode={mode} />
		</span>
	);
}
