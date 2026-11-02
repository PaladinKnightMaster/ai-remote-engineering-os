// Package setup is services/api's first-run operator setup (ADR-0078; v0.17 FRX-1/2/3,
// SEC-64): a one-time setup code that only root on the server can print
// (`api setup-code issue`) gates the creation of the FIRST and ONLY operator through
// the browser. It is deliberately NOT self-service signup (ADR-0046): provisioning stays
// anchored to whoever can run a command on the host, and the code is never a login
// credential. Mirrors ADR-0068 §D6's one-time-secret pattern (internal/pairing): hash
// only, constant-time compare, consumed in the same transaction that grants access,
// capped attempts, operator-initiated audit events only.
//
// Self-contained like internal/pairing and internal/grant: it depends on the pgx pool,
// the outbox writer and the account type, never on api's other internals.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/account"
)

const (
	// CodeLifetime is the code's validity window (ADR-0078 §D1).
	CodeLifetime = 60 * time.Minute
	// MaxAttempts wrong completions invalidate the code (ADR-0078 §D1): the Nth failure
	// sets invalidated_at in the SAME transaction as the increment.
	MaxAttempts = 5
	// MinPasswordLen is the minimum password length in characters (ADR-0078 §D3).
	MinPasswordLen = 12
	maxPasswordLen = 1024 // bounds argon2id work on an unauthenticated route

	// Crockford base32: no I, L, O, U. Normalize maps the O/I/L aliases back.
	alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	codeLen  = 20 // 20 * 5 bits = 100 bits

	setupEdge   = "human-agent"
	issuedActor = "system:setup" // no operator exists yet; the issuer is root on the host
)

var (
	// ErrOperatorExists: an operator already exists, so setup is complete.
	ErrOperatorExists = errors.New("an operator already exists; setup is complete")
	// ErrInvalidCode collapses EVERY code refusal -- wrong, expired, used, invalidated,
	// locked, none issued -- into one answer so it is not an oracle (SEC-64).
	ErrInvalidCode = errors.New("setup code is invalid or expired")
	// ErrNoOperator: the operators table is empty.
	ErrNoOperator = errors.New("no operator has been set up")
)

// InputError is a refusal of the username/password the caller chose. It carries no
// secret and is returned BEFORE any code attempt is counted.
type InputError struct{ Reason string }

func (e *InputError) Error() string { return e.Reason }

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{2,63}$`)

// ValidateCredentials checks the chosen username and password. '#' and ':' are
// excluded from usernames on purpose: the subject is "operator:<username>" and a paired
// approval chains as "<subject>#paired" (server.pairingResolvedBy).
func ValidateCredentials(username, password string) error {
	if !usernameRE.MatchString(username) {
		return &InputError{"username must be 3-64 characters: letters, digits, '.', '_', '@' or '-'"}
	}
	if n := utf8.RuneCountInString(password); n < MinPasswordLen {
		return &InputError{fmt.Sprintf("password must be at least %d characters", MinPasswordLen)}
	}
	if len(password) > maxPasswordLen {
		return &InputError{"password is too long"}
	}
	return nil
}

// GenerateCode returns a fresh 20-character Crockford base32 code from crypto/rand,
// formatted as four groups of five (XXXXX-XXXXX-XXXXX-XXXXX). 32 symbols divide 256
// evenly, so masking a random byte to 5 bits is unbiased.
func GenerateCode() (string, error) {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate setup code: %w", err)
	}
	var b strings.Builder
	for i, v := range raw {
		if i > 0 && i%5 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(alphabet[v&31])
	}
	return b.String(), nil
}

// Normalize makes typing forgiving: case-insensitive, spaces and dashes stripped,
// Crockford's aliases O->0 and I/L->1.
func Normalize(in string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		switch r {
		case ' ', '\t', '-':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hashCode(code string) [sha256.Size]byte { return sha256.Sum256([]byte(Normalize(code))) }

// Store persists setup_codes + operators.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
	now    func() time.Time // injectable so expiry is testable without sleeping
}

// NewStore builds the setup store; outboxTable is the api outbox ("api_outbox").
func NewStore(pool *pgxpool.Pool, outboxTable string) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: outboxTable}, now: time.Now}
}

// inTx runs fn in a transaction: commit when fn returns nil, roll back otherwise.
func (s *Store) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) emit(ctx context.Context, tx pgx.Tx, actor, target, outcome string) error {
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: s.now(), Actor: actor, Edge: setupEdge, Target: target, Outcome: outcome,
	}); err != nil {
		return fmt.Errorf("enqueue %s: %w", outcome, err)
	}
	return nil
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func operatorExists(ctx context.Context, q queryer) (bool, error) {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operators)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check operator: %w", err)
	}
	return exists, nil
}

// Issue invalidates any previous live code, stores the hash of a fresh one, and emits
// setup.code.issued (no code, no hash) -- one transaction. It returns the PLAINTEXT
// code, the only place it ever exists outside the operator's head. Refused once an
// operator exists (ADR-0078 §D1: --new-setup-code works only while none does).
func (s *Store) Issue(ctx context.Context) (string, error) {
	code, err := GenerateCode()
	if err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("mint setup code id: %w", err)
	}
	sum := hashCode(code)
	now := s.now()
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if exists, err := operatorExists(ctx, tx); err != nil {
			return err
		} else if exists {
			return ErrOperatorExists
		}
		if _, err := tx.Exec(ctx, `
			UPDATE setup_codes SET invalidated_at = $1
			 WHERE consumed_at IS NULL AND invalidated_at IS NULL`, now); err != nil {
			return fmt.Errorf("invalidate previous setup code: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO setup_codes (id, code_hash, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
			id.String(), sum[:], now.Add(CodeLifetime), now); err != nil {
			return fmt.Errorf("insert setup code: %w", err)
		}
		return s.emit(ctx, tx, issuedActor, "setup-code:"+id.String(), "setup.code.issued")
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// OperatorExists reports whether the browser-created operator exists. (The caller also
// checks the environment operator; env always wins, ADR-0078 §D4.)
func (s *Store) OperatorExists(ctx context.Context) (bool, error) {
	return operatorExists(ctx, s.pool)
}

// Operator loads the table's operator for authentication; ErrNoOperator if none.
func (s *Store) Operator(ctx context.Context) (*account.Operator, error) {
	var u, h string
	err := s.pool.QueryRow(ctx, `SELECT username, password_hash FROM operators`).Scan(&u, &h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoOperator
	}
	if err != nil {
		return nil, fmt.Errorf("load operator: %w", err)
	}
	return account.New(u, h), nil
}

// Complete is the whole of first-run setup in ONE transaction (ADR-0078 §D3): verify
// the code's hash, expiry and attempts, hash the password (argon2id), insert the
// operator, consume the code and emit setup.completed (actor operator:<username>,
// clientAddr as the target). clientAddr is display data the caller has already
// sanitized.
//
// Every refusal of the CODE is ErrInvalidCode. A wrong guess increments attempts and
// the MaxAttempts-th invalidates the code and emits setup.code.locked; that state is
// committed even though the call fails. The code row is locked FOR UPDATE, so
// concurrent completions serialize: the loser finds the code consumed (ErrInvalidCode)
// and, were it to get past that, the operators primary key rejects the second row.
// The password is hashed only AFTER the code verified, so an unauthenticated caller
// cannot make api spend argon2id memory.
func (s *Store) Complete(ctx context.Context, code, username, password, clientAddr string) (*account.Operator, error) {
	if err := ValidateCredentials(username, password); err != nil {
		return nil, err // input errors never touch (or count against) the code
	}
	var op *account.Operator
	var wrongGuess bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if exists, err := operatorExists(ctx, tx); err != nil {
			return err
		} else if exists {
			return ErrOperatorExists
		}
		var (
			id        string
			stored    []byte
			expiresAt time.Time
			attempts  int
		)
		err := tx.QueryRow(ctx, `
			SELECT id, code_hash, expires_at, attempts FROM setup_codes
			 WHERE consumed_at IS NULL AND invalidated_at IS NULL FOR UPDATE`).
			Scan(&id, &stored, &expiresAt, &attempts)
		now := s.now()
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !expiresAt.After(now)) {
			return ErrInvalidCode // none live, or expired: nothing to count against
		}
		if err != nil {
			return fmt.Errorf("lock setup code: %w", err)
		}
		got := hashCode(code)
		if subtle.ConstantTimeCompare(got[:], stored) != 1 {
			attempts++
			if attempts >= MaxAttempts {
				if _, err := tx.Exec(ctx, `UPDATE setup_codes SET attempts = $2, invalidated_at = $3 WHERE id = $1`,
					id, attempts, now); err != nil {
					return fmt.Errorf("invalidate setup code on attempt cap: %w", err)
				}
				if err := s.emit(ctx, tx, issuedActor, "setup-code:"+id, "setup.code.locked"); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `UPDATE setup_codes SET attempts = $2 WHERE id = $1`, id, attempts); err != nil {
				return fmt.Errorf("increment setup attempts: %w", err)
			}
			wrongGuess = true
			return nil // commit the counter; the caller sees ErrInvalidCode below
		}

		hash, err := auth.HashPassword(password)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO operators (username, password_hash, created_at) VALUES ($1, $2, $3)`,
			username, hash, now); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23505" {
				return ErrOperatorExists // lost the race for the single row
			}
			return fmt.Errorf("insert operator: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE setup_codes SET consumed_at = $2 WHERE id = $1`, id, now); err != nil {
			return fmt.Errorf("consume setup code: %w", err)
		}
		if clientAddr == "" {
			clientAddr = "unknown"
		}
		if err := s.emit(ctx, tx, "operator:"+username, "setup-client:"+clientAddr, "setup.completed"); err != nil {
			return err
		}
		op = account.New(username, hash)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if wrongGuess {
		return nil, ErrInvalidCode
	}
	return op, nil
}
