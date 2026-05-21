// Package account holds the single bootstrap operator account (ADR-0046: no
// self-service signup, no user CRUD beyond this one account at P0). Credentials
// come from the environment (provisioned by install.sh / authctl); the password
// is stored only as an argon2id hash.
//
// (ADR-0046 suggested the path services/api/internal/auth/; named `account` here
// to avoid confusion with the imported packages/go/auth crypto module.)
package account

import (
	"errors"
	"os"

	"aireos.local/packages/go/auth"
)

// ErrNoBootstrap means the bootstrap operator env vars are not set.
var ErrNoBootstrap = errors.New("no bootstrap operator configured (set AIREOS_BOOTSTRAP_OPERATOR_USER and AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH)")

// Operator is the single bootstrap operator.
type Operator struct {
	Username     string
	passwordHash string // argon2id PHC string
}

// New builds an Operator from a username and an argon2id PHC hash.
func New(username, passwordHash string) *Operator {
	return &Operator{Username: username, passwordHash: passwordHash}
}

// FromEnv loads the bootstrap operator from AIREOS_BOOTSTRAP_OPERATOR_USER and
// AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH (argon2id PHC, e.g. `authctl hash`).
func FromEnv() (*Operator, error) {
	u := os.Getenv("AIREOS_BOOTSTRAP_OPERATOR_USER")
	h := os.Getenv("AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH")
	if u == "" || h == "" {
		return nil, ErrNoBootstrap
	}
	return New(u, h), nil
}

// Authenticate returns the operator subject id when username + password match.
// The password hash is always verified (even on a username miss) to keep the
// timing uniform and avoid user enumeration.
func (o *Operator) Authenticate(username, password string) (subject string, ok bool) {
	pwOK := auth.VerifyPassword(password, o.passwordHash) == nil
	if username == o.Username && pwOK {
		return "operator:" + o.Username, true
	}
	return "", false
}
