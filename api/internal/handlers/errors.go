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
	// ErrCredentialCheckViolation is returned when an INSERT/UPDATE
	// trips a CHECK constraint (PG SQLSTATE 23514). With Epic 55 the
	// `kind` and `slug` columns carry CHECK constraints; the handler
	// layer should ideally have caught the bad input via ValidateKind /
	// ValidateSlug at the boundary, so reaching this code is a sign
	// that boundary validation drifted from the DB constraint set
	// (the property tests in credential_identity_test.go guard against
	// that drift). Map to 400 anyway as defense-in-depth — the user
	// sent invalid data, not a server fault.
	ErrCredentialCheckViolation = errors.New("credential failed validation")
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
		case "23514":
			return ErrCredentialCheckViolation
		case "P0002":
			return ErrCredentialNotFound
		}
	}
	return err
}
