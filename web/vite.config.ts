import babel from "@rolldown/plugin-babel";
import tailwindcss from "@tailwindcss/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import path from "path";
import { defineConfig } from "vite";
import svgr from "vite-plugin-svgr";

const dirname = import.meta.dirname;

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
			allow: [path.resolve(dirname, ".."), path.resolve(dirname)],
		},
	},
	resolve: {
		alias: {
			// Generated protobuf/connect code lives at <repo>/gen/ts, outside web/,
			// so it is aliased separately from "@" (which means web/src).
			"@gen": path.resolve(dirname, "../gen/ts"),
			"@": path.resolve(dirname, "./src"),
		},
	},
	build: {
		chunkSizeWarningLimit: 1000,
	},
});
