import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import path from "path";
import { defineConfig } from "vite";
import svgr from "vite-plugin-svgr";

// https://vite.dev/config/
export default defineConfig({
	plugins: [
		react({
			babel: {
				plugins: ["babel-plugin-react-compiler"],
			},
		}),
		tailwindcss(),
		svgr(),
	],
	resolve: {
		alias: {
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

					// Radix UI components
					if (id.includes("@radix-ui")) {
						return "vendor-radix";
					}

					// Tanstack libraries
					if (id.includes("@tanstack")) {
						return "vendor-tanstack";
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
