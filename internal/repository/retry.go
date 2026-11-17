package repository

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrWorkspaceNotInstalled = errors.New("workspace_not_installed")

type permanentError struct{ error }

func (permanentError) Permanent() bool { return true }
func (e permanentError) Unwrap() error { return e.error }
func Permanent(err error) error        { return permanentError{err} }

func IsPermanent(err error) bool {
	var classified interface{ Permanent() bool }
	if errors.As(err, &classified) && classified.Permanent() {
		return true
	}
	var database *pgconn.PgError
	// Constraint violations cannot be fixed by replaying the same payload.
	return errors.As(err, &database) && len(database.Code) == 5 && database.Code[:2] == "23"
}
