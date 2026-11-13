package clickhouseerr

import (
	"errors"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	codeUnknownDatabase      = 81
	codeRequiredPassword     = 194
	codeAccessDenied         = 497
	codeAuthenticationFailed = 516
)

var rejectedCodes = map[int32]bool{
	codeUnknownDatabase:      true,
	codeRequiredPassword:     true,
	codeAccessDenied:         true,
	codeAuthenticationFailed: true,
}

// CredentialsRejected reports whether ClickHouse rejected the credentials or
// the configured database, which no number of retries fixes.
func CredentialsRejected(err error) bool {
	exception, ok := errors.AsType[*clickhouse.Exception](err)
	return ok && rejectedCodes[exception.Code]
}
