// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func TestClassifyPostgresError_ErrNoRows(t *testing.T) {
	// pgx.ErrNoRows is what production stores return for not-found deletes.
	// Regression guard for the 404->500 bug: this must map to ErrCredentialNotFound.
	got := ClassifyPostgresError(pgx.ErrNoRows)
	assert.ErrorIs(t, got, ErrCredentialNotFound)
}

func TestClassifyPostgresError_DuplicatePgError(t *testing.T) {
	got := ClassifyPostgresError(&pgconn.PgError{Code: "23505"})
	assert.ErrorIs(t, got, ErrDuplicateCredential)
}

func TestClassifyPostgresError_NotFoundPgError(t *testing.T) {
	// PL/pgSQL RAISE EXCEPTION ... USING ERRCODE 'P0002' still maps to not-found.
	got := ClassifyPostgresError(&pgconn.PgError{Code: "P0002"})
	assert.ErrorIs(t, got, ErrCredentialNotFound)
}

func TestClassifyPostgresError_GenericErrorUnchanged(t *testing.T) {
	orig := errors.New("connection refused")
	got := ClassifyPostgresError(orig)
	assert.Equal(t, orig, got)
}

func TestClassifyPostgresError_NilReturnsNil(t *testing.T) {
	assert.Nil(t, ClassifyPostgresError(nil))
}
