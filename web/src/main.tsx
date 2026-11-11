import { createRoot } from "react-dom/client";
import { logCaughtError } from "@/lib/not-found";
import App from "./App.tsx";
import "./index.css";

const rootElement = document.getElementById("root");
if (rootElement) {
	createRoot(rootElement, {
		onCaughtError: logCaughtError,
	}).render(<App />);
}
// throwaway
