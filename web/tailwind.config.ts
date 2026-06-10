import type { Config } from "tailwindcss";

export default {
	content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
	theme: {
		extend: {
			fontFamily: {
				sans: ["Satoshi", "Inter", "system-ui", "sans-serif"],
				mono: ["DM Mono", "JetBrains Mono", "Menlo", "Monaco", "monospace"],
				serif: ["DM Serif Display", "Georgia", "serif"],
			},
			colors: {
				primary: {
					DEFAULT: "#C7654F",
					hover: "#B55942",
					light: "#FBF3F1",
					border: "#E8C5BC",
				},
				sage: {
					DEFAULT: "#6B8E7F",
					hover: "#5A7A6D",
					light: "#F2F6F4",
					border: "#C9D9D2",
				},
				sand: {
					DEFAULT: "#A88F6F",
					light: "#F8F5F0",
					border: "#DDD5CA",
				},
				success: {
					DEFAULT: "#52796F",
					light: "#EEF3F1",
					border: "#C5D5CF",
				},
				warning: {
					DEFAULT: "#B8956A",
					light: "#F9F6F0",
					border: "#E5DCCF",
				},
				error: {
					DEFAULT: "#A85751",
					light: "#F8F1F0",
					border: "#E2C9C6",
				},
				info: {
					DEFAULT: "#6B8AAA",
					light: "#F0F4F7",
					border: "#C9D6E2",
				},
				"bg-canvas": "#FDFCFA",
				"bg-elevated": "#FFFFFF",
				"bg-subtle": "#F7F5F2",
				"bg-hover": "#F0EDE8",
				"bg-muted": "#E8E5E0",
				"text-primary": "#1C1917",
				"text-secondary": "#57534E",
				"text-tertiary": "#78716C",
				"text-quaternary": "#A8A29E",
				"border-subtle": "#E7E5E0",
				"border-default": "#D6D3CE",
				"border-strong": "#A8A29E",
			},
			boxShadow: {
				xs: "0 1px 2px rgba(28, 25, 23, 0.03)",
				sm: "0 2px 4px rgba(28, 25, 23, 0.04), 0 1px 1px rgba(28, 25, 23, 0.02)",
				md: "0 4px 8px rgba(28, 25, 23, 0.05), 0 2px 4px rgba(28, 25, 23, 0.03)",
				lg: "0 8px 16px rgba(28, 25, 23, 0.06), 0 4px 8px rgba(28, 25, 23, 0.04)",
				xl: "0 12px 24px rgba(28, 25, 23, 0.08), 0 6px 12px rgba(28, 25, 23, 0.05)",
			},
			borderRadius: {
				sm: "6px",
				DEFAULT: "8px",
				md: "10px",
				lg: "12px",
				xl: "14px",
			},
			transitionDuration: {
				fast: "180ms",
				medium: "220ms",
				slow: "350ms",
			},
			transitionTimingFunction: {
				smooth: "cubic-bezier(0.4, 0, 0.2, 1)",
			},
		},
	},
} satisfies Config;
