import babel from "@rolldown/plugin-babel";
import tailwindcss from "@tailwindcss/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import path from "path";
import { defineConfig } from "vite";
import svgr from "vite-plugin-svgr";

// https://vite.dev/config/
export default defineConfig({
	plugins: [
		react(),
		babel({ presets: [reactCompilerPreset()] }),
		tailwindcss(),
		svgr(),
	],
	server: {
		fs: {
			// gen/ts is outside the vite root; allow the dev server to read it.
			allow: [path.resolve(__dirname, ".."), path.resolve(__dirname)],
		},
	},
	resolve: {
		alias: {
			// Generated protobuf/connect code lives at <repo>/gen/ts, outside web/,
			// so it is aliased separately from "@" (which means web/src).
			"@gen": path.resolve(__dirname, "../gen/ts"),
			"@": path.resolve(__dirname, "./src"),
		},
	},
	build: {
		rollupOptions: {
			output: {
				manualChunks(id) {
					if (id.includes("node_modules/react/")) {
						return "vendor-react-core";
					}
					if (id.includes("node_modules/react-dom/")) {
						return "vendor-react-dom";
					}

					// Base UI components
					if (id.includes("@base-ui/react")) {
						return "vendor-base-ui";
					}

					// Tanstack libraries
					if (id.includes("@tanstack")) {
						return "vendor-tanstack";
					}

					// Recharts + its d3/victory deps
					if (
						id.includes("recharts") ||
						id.includes("victory-vendor") ||
						id.includes("d3-")
					) {
						return "vendor-recharts";
					}

					// Other node_modules
					if (id.includes("node_modules")) {
						return "vendor-other";
					}
				},
			},
		},
		chunkSizeWarningLimit: 1000,
	},
});
