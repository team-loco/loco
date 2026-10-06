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
		rolldownOptions: {
			output: {
				strictExecutionOrder: true,
				codeSplitting: {
					groups: [
						{
							name: "vendor-react",
							test: /node_modules[\\/](react|react-dom|scheduler|use-sync-external-store)[\\/]/,
							priority: 40,
						},
						{
							name: "vendor-base-ui",
							test: /node_modules[\\/]@(base-ui|floating-ui)[\\/]/,
							priority: 30,
						},
						{
							name: "vendor-tanstack",
							test: /node_modules[\\/]@tanstack[\\/]/,
							priority: 30,
						},
						{
							name: "vendor",
							test: /node_modules[\\/]/,
							tags: ["$initial"],
							priority: 20,
						},
						{
							name: "vendor-recharts",
							test: /node_modules[\\/]recharts[\\/]/,
							priority: 10,
						},
						{
							name: "vendor-sugar-high",
							test: /node_modules[\\/]sugar-high[\\/]/,
							priority: 10,
						},
					],
				},
			},
		},
		chunkSizeWarningLimit: 1000,
	},
});
