import "@fontsource-variable/geist/index.css";
import "@fontsource-variable/geist-mono/index.css";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import "./index.css";

const rootElement = document.getElementById("root");
if (rootElement) {
	createRoot(rootElement).render(<App />);
}
