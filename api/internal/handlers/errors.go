// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

var (
	ErrDuplicateCredential = errors.New("duplicate credential")
	ErrCredentialNotFound  = errors.New("credential not found")
)

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

func isDuplicateErr(err error) bool {
	return errors.Is(err, ErrDuplicateCredential) || errors.Is(err, secrets.ErrDuplicateSecret)
}
