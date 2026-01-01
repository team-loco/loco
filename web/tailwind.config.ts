import type { Config } from "tailwindcss";

export default {
	content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
	theme: {
		extend: {
			fontFamily: {
				sans: ["Space Grotesk", "Inter", "system-ui", "sans-serif"],
			},
			colors: {
				"success-soft": "#eaf3e6",
				"success-border": "#b4cea4",
			},
		},
	},
} satisfies Config;
