package secrets

import "errors"

// Sentinel errors returned by the secrets package. Callers should
// prefer errors.Is to substring matching on Error() strings; the
// strings are for humans only and may change.
//
// Wrapping (`fmt.Errorf("...: %w", ErrSecretNotFound)`) is supported
// and recommended so the underlying classification survives upstream
// formatting.
var (
	// ErrSecretNotFound is returned when a secret does not exist or
	// is not owned by the requesting user. Both cases are conflated
	// to avoid leaking workspace existence cross-user.
	ErrSecretNotFound = errors.New("secret not found")

	// ErrDuplicateSecret is returned when CreateSecret would violate
	// the (user_id, name) uniqueness constraint.
	ErrDuplicateSecret = errors.New("duplicate secret")

	// ErrDEKUnavailable is returned when the per-session DEK is not
	// in the cache (typically because the JWT's jti has expired or
	// the user has not logged in since the cache was flushed).
	// The HTTP layer maps this to 403 + a "re-authenticate" message.
	ErrDEKUnavailable = errors.New("DEK unavailable")

	// ErrInvalidSecretType is returned when a CreateSecret request
	// names a type outside ValidSecretTypes.
	ErrInvalidSecretType = errors.New("invalid secret type")

	// ErrInvalidMetadata is returned when the metadata blob is
	// missing a required field for the secret type, fails JSON
	// validation, or contains an adversarial mount_path.
	ErrInvalidMetadata = errors.New("invalid secret metadata")

	// ErrInvalidPassword is returned by RevealSecret when the
	// password reconfirmation step fails. The handler maps this to
	// a uniform 403 — the same status used for missing DEK — so
	// the response shape does not differentiate between "wrong
	// password" and "session expired", reducing what an attacker
	// who has stolen a JWT can learn.
	ErrInvalidPassword = errors.New("invalid password")
)
