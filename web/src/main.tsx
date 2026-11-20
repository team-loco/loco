import { createRoot } from "react-dom/client";
import { logCaughtError } from "@/lib/not-found";
import { addThemeFonts } from "@/lib/theme-stylesheet";
import { showTitlesAsTooltips } from "@/lib/title-tooltip";
import { watchForUpdates } from "@/lib/update-check";
import App from "./App.tsx";
import "./index.css";

const rootElement = document.getElementById("root");
if (rootElement) {
	createRoot(rootElement, {
		onCaughtError: logCaughtError,
	}).render(<App />);
}
addThemeFonts();
showTitlesAsTooltips();
watchForUpdates();
