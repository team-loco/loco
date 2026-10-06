package loco

import (
	"context"
	"errors"
	"image/color"
	"io"
	"os"
	runtimeDebug "runtime/debug"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/team-loco/loco/internal/ui"
)

// LocoColorScheme is a color scheme inspired by the Southern Pacific 4449.
func LocoColorScheme() fang.ColorSchemeFunc {
	return func(ldf lipgloss.LightDarkFunc) fang.ColorScheme {
		return fang.ColorScheme{
			Base:           ldf(ui.LocoLightGray, ui.LocoDarkGray),
			Title:          ldf(ui.LocoRed, ui.LocoOrange),
			Description:    ldf(ui.LocoMuted, ui.LocoSteel),
			Codeblock:      ldf(ui.LocoLightGrey, ui.LocoDeepCoal),
			Program:        ldf(ui.LocoOrange, ui.LocoOrange),
			DimmedArgument: ldf(ui.LocoDimGrey, ui.LocoMidGrey),
			Comment:        ldf(ui.LocoGreyish, ui.LocoDarkGrey),
			Flag:           ldf(ui.LocoOrange, ui.LocoOrange),
			FlagDefault:    ldf(ui.LocoSteel, ui.LocoDimGrey),
			Command:        ldf(ui.LocoRed, ui.LocoOrange),
			QuotedString:   ldf(ui.LocoGreen, ui.LocoGreen),
			Argument:       ldf(ui.LocoCyan, ui.LocoCyan),
			Help:           ldf(ui.LocoDimGrey, ui.LocoMidGrey),
			Dash:           ldf(ui.LocoOrange, ui.LocoOrange),
			ErrorHeader: [2]color.Color{
				ldf(ui.LocoWhite, ui.LocoWhite),
				ldf(ui.LocoRed, ui.LocoRed),
			},
			ErrorDetails: ldf(ui.LocoRed, ui.LocoOrange),
		}
	}
}

var version string

func moduleVersion() string {
	i, ok := runtimeDebug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	return i.Main.Version
}

func Cli() {
	if version == "" {
		version = moduleVersion()
	}

	env := NewEnv()
	ctx := context.Background()
	root := NewRootCmd(env)
	err := fang.Execute(ctx,
		root,
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(LocoColorScheme()),
		fang.WithErrorHandler(handleError))
	env.versionCheck.report(os.Stderr)
	if err != nil {
		os.Exit(1)
	}
}

func handleError(w io.Writer, styles fang.Styles, err error) {
	fang.DefaultErrorHandler(w, styles, displayError(err))
}

func displayError(err error) error {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return err
	}
	message := connectErr.Message()
	if message == "" {
		message = connectErr.Code().String()
	}
	if connectErr.Code() == connect.CodeUnavailable {
		message = "could not reach the Loco API: " + message
	}
	text := strings.Replace(err.Error(), connectErr.Error(), message, 1)
	return errors.New(text)
}
