import type { Config } from "tailwindcss";

export default {
	content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
	theme: {
		extend: {
			fontFamily: {
				sans: ["Space Grotesk", "Inter", "system-ui", "sans-serif"],
			},
		},
	},
} satisfies Config;
