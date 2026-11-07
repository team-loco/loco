package ui

import (
	"errors"
	"image/color"
	"io"
	"log/slog"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect/v2"
)

func ColorScheme() fang.ColorSchemeFunc {
	return func(ldf lipgloss.LightDarkFunc) fang.ColorScheme {
		return fang.ColorScheme{
			Base:           Fg.Resolve(ldf),
			Title:          Accent.Resolve(ldf),
			Description:    Fg2.Resolve(ldf),
			Codeblock:      Bg3.Resolve(ldf),
			Program:        Logo.Resolve(ldf),
			DimmedArgument: Fg4.Resolve(ldf),
			Comment:        Comment.Resolve(ldf),
			Flag:           Accent.Resolve(ldf),
			FlagDefault:    Fg3.Resolve(ldf),
			Command:        Accent.Resolve(ldf),
			QuotedString:   String.Resolve(ldf),
			Argument:       Link.Resolve(ldf),
			Help:           Fg3.Resolve(ldf),
			Dash:           Fg3.Resolve(ldf),
			ErrorHeader: [2]color.Color{
				BadFg.Resolve(ldf),
				BadBg.Resolve(ldf),
			},
			ErrorDetails: Bad.Resolve(ldf),
		}
	}
}

func HandleError(w io.Writer, styles fang.Styles, err error) {
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
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
