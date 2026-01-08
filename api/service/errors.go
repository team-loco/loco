package service

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrImproperUsage = errors.New("improper usage of the api")

// isPgConstraintViolation checks if an error is a PostgreSQL unique constraint violation
func isPgConstraintViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}
