package main

import "log/slog"

var redactedLogKeys = map[string]struct{}{
	"env":      {},
	"values":   {},
	"data":     {},
	"secret":   {},
	"token":    {},
	"password": {},
}

func redactAttr(_ []string, attr slog.Attr) slog.Attr {
	if _, drop := redactedLogKeys[attr.Key]; drop {
		return slog.Attr{}
	}
	return attr
}
