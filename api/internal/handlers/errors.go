// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrDuplicateCredential = errors.New("duplicate credential")
	ErrCredentialNotFound  = errors.New("credential not found")
)

func isDuplicateErr(err error) bool {
	return errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential)
}

func ClassifyPostgresError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCredentialNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrDuplicateCredential
		case "P0002":
			return ErrCredentialNotFound
		}
	}
	return err
}
