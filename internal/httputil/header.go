package httputil

type HeaderGetter interface {
	Get(string) string
}
