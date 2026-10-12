package main

import (
	"net/http"
	"slices"

	connectcors "connectrpc.com/cors"
	"github.com/rs/cors"
)

func withCORS(origins []string, handler http.Handler) http.Handler {
	options := cors.Options{
		AllowOriginFunc:  func(origin string) bool { return slices.Contains(origins, origin) },
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   append(connectcors.AllowedHeaders(), "Authorization"),
		ExposedHeaders:   connectcors.ExposedHeaders(),
		AllowCredentials: true,
	}
	middleware := cors.New(options)
	return middleware.Handler(handler)
}
