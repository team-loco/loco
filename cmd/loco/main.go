package loco

import (
	"context"
	"errors"
	"image/color"
	"io"
	"log/slog"
	"os"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/ui"
)

func LocoColorScheme() fang.ColorSchemeFunc {
	return func(ldf lipgloss.LightDarkFunc) fang.ColorScheme {
		return fang.ColorScheme{
			Base:           ui.Fg.Resolve(ldf),
			Title:          ui.Accent.Resolve(ldf),
			Description:    ui.Fg2.Resolve(ldf),
			Codeblock:      ui.Bg3.Resolve(ldf),
			Program:        ui.Logo.Resolve(ldf),
			DimmedArgument: ui.Fg4.Resolve(ldf),
			Comment:        ui.Comment.Resolve(ldf),
			Flag:           ui.Accent.Resolve(ldf),
			FlagDefault:    ui.Fg3.Resolve(ldf),
			Command:        ui.Accent.Resolve(ldf),
			QuotedString:   ui.String.Resolve(ldf),
			Argument:       ui.Link.Resolve(ldf),
			Help:           ui.Fg3.Resolve(ldf),
			Dash:           ui.Fg3.Resolve(ldf),
			ErrorHeader: [2]color.Color{
				ui.BadFg.Resolve(ldf),
				ui.BadBg.Resolve(ldf),
			},
			ErrorDetails: ui.Bad.Resolve(ldf),
		}
	}
}

func Cli(version string) {
	ui.DetectBackground()
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
		os.Exit(cmdutil.ExitCode(err))
	}
}

func handleError(w io.Writer, styles fang.Styles, err error) {
	if exitErr, ok := errors.AsType[*cmdutil.ExitError](err); ok {
		slog.Debug("command ended without a message", "code", exitErr.Code)
		return
	}
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
