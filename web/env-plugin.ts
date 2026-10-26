import type { BunPlugin } from "bun";

const publicEnv = ["PUBLIC_API_URL", "PUBLIC_APP_ENV"];

const envPlugin: BunPlugin = {
	name: "loco-env",
	setup(build) {
		build.onResolve({ filter: /^loco:env$/ }, () => ({ path: "loco:env", namespace: "loco-env" }));
		build.onLoad({ filter: /.*/, namespace: "loco-env" }, () => ({
			contents: publicEnv
				.map((name) => `export const ${name} = ${JSON.stringify(process.env[name] ?? "")};`)
				.join("\n"),
			loader: "js",
		}));
	},
};

export default envPlugin;
