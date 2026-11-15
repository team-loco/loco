import { useEffect } from "react";

import dmSansLatinExt from "@fontsource-variable/dm-sans/files/dm-sans-latin-ext-wght-normal.woff2" with { type: "file" };
import dmSansLatin from "@fontsource-variable/dm-sans/files/dm-sans-latin-wght-normal.woff2" with { type: "file" };

import catppuccin from "../themes/catppuccin.css" with { type: "file" };
import gruvbox from "../themes/gruvbox.css" with { type: "file" };
import neobrutal from "../themes/neobrutal.css" with { type: "file" };
import paper from "../themes/paper.css" with { type: "file" };
import rosePine from "../themes/rose-pine.css" with { type: "file" };
import tokyoNight from "../themes/tokyo-night.css" with { type: "file" };
import { defaultColorTheme, type ColorTheme } from "./theme";

const stylesheets: Record<Exclude<ColorTheme, typeof defaultColorTheme>, string> = {
	catppuccin,
	"rose-pine": rosePine,
	gruvbox,
	"tokyo-night": tokyoNight,
	paper,
	neobrutal,
};

const latin =
	"U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+20AC, U+2122, U+2191, U+2193, U+2212, U+2215, U+FEFF, U+FFFD";
const latinExt =
	"U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF, U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020, U+20A0-20AB, U+20AD-20C0, U+2113, U+2C60-2C7F, U+A720-A7FF";

let dmSansAdded = false;

// The browser downloads a registered face only when text set in it renders.
function addDmSans() {
	if (dmSansAdded) return;
	dmSansAdded = true;
	for (const [src, unicodeRange] of [
		[dmSansLatin, latin],
		[dmSansLatinExt, latinExt],
	] as const) {
		document.fonts.add(
			new FontFace("DM Sans Variable", `url(${src}) format("woff2")`, {
				weight: "100 1000",
				display: "swap",
				unicodeRange,
			}),
		);
	}
}

/** Loads a colour theme's stylesheet; React adds each one once and holds the render until it has loaded. */
export function ThemeStylesheet({ color }: { color: ColorTheme }) {
	useEffect(() => {
		if (color === "neobrutal") addDmSans();
	}, [color]);
	if (color === defaultColorTheme) return null;
	return <link rel="stylesheet" href={stylesheets[color]} precedence="theme" />;
}
