// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrDuplicateCredential = errors.New("duplicate credential")
	ErrCredentialNotFound  = errors.New("credential not found")
)

func ClassifyPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrDuplicateCredential
		case "02000", "P0002":
			return ErrCredentialNotFound
		}
	}
	return err
}
