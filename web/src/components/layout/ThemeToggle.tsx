import { useTheme } from "../../lib/use-theme";
import "./ThemeToggle.css";

const ThemeToggle = () => {
	const { theme, toggleTheme } = useTheme();
	console.log("theme", theme);
	const isDark = theme === "dark";

	const playSound = async () => {
		new window.AudioContext(); // necessary fix audio delay on Safari

		const audio = new Audio(`${isDark ? "/lightMode.wav" : "/darkMode.wav"}`);
		audio.volume = 0.9;
		await audio.play();
	};

	const handleToggle = async () => {
		toggleTheme();
		await playSound();
	};

	return (
		<button
			onClick={() => void handleToggle()}
			type="button"
			aria-label={isDark ? "Activate Light Mode" : "Activate Dark Mode"}
			title={isDark ? "Activate Light Mode" : "Activate Dark Mode"}
			className="flex items-center gap-4 w-full px-2 py-2 rounded-md hover:bg-sidebar-accent text-sm"
		>
			{isDark ? (
				<div className="div-toggle-btn-dark border-0 shadow-none h-4 w-4"></div>
			) : (
				<div className="div-toggle-btn-light border-0 shadow-none h-4 w-4"></div>
			)}
			<span>{isDark ? "Light Mode" : "Dark Mode"}</span>
		</button>
	);
};

export { ThemeToggle };
