package ui

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
)

type Color struct {
	Light color.Color
	Dark  color.Color
}

var darkBackground = true

func DetectBackground() {
	darkBackground = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}

func (c Color) RGBA() (uint32, uint32, uint32, uint32) {
	if darkBackground {
		return c.Dark.RGBA()
	}
	return c.Light.RGBA()
}

func (c Color) Resolve(ldf lipgloss.LightDarkFunc) color.Color {
	return ldf(c.Light, c.Dark)
}

func pair(light, dark string) Color {
	return Color{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

var (
	Fg    = pair("#2a2522", "#f3efea")
	Fg2   = pair("#56504a", "#c9c2ba")
	Fg3   = pair("#7d766e", "#8f877e")
	Fg4   = pair("#aaa39b", "#5e5750")
	Bg3   = pair("#f1efed", "#272420")
	Line  = pair("#e8e5e1", "#2f2b27")
	Line2 = pair("#d3cfc9", "#403b35")

	Accent   = pair("#1e40af", "#4a74d6")
	OnAccent = pair("#ffffff", "#ffffff")
	Logo     = pair("#2455eb", "#6f8ff2")
	Link     = pair("#2c4a99", "#8aa6e6")

	Ok   = pair("#1d5a36", "#8fe0a8")
	Info = pair("#23448a", "#9cc3f5")
	Warn = pair("#d97706", "#e89a3c")
	Bad  = pair("#b3261e", "#f0665c")

	BadFg = pair("#7d2119", "#f6a3a0")
	BadBg = pair("#fbe1de", "#46181a")

	String  = pair("#1d6b3c", "#93d4a5")
	Comment = pair("#958d84", "#766e66")
)
