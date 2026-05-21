// Package chain implements the audit-service tamper-evidence chain (ADR-0045).
// audit-service is the sole chain authority: only it computes prevHash and
// chainHash, in-process, inside the same transaction as the row insert.
package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"aireos.local/services/audit-service/internal/audit"
)

// Genesis is the prevHash of the first event in the chain: 64 hex zeros
// (fixed-width sentinel).
const Genesis = "0000000000000000000000000000000000000000000000000000000000000000"

// Compute returns the chainHash for an event linked to prevHash:
//
//	chainHash = SHA256( prevHash_bytes || framed(core fields) )
//
// Each core field is length-prefixed (uint32 big-endian length + bytes) before
// concatenation, so no field value can be shifted across a boundary to forge a
// colliding pre-image (concatenation-injection safe). The field order is fixed
// and includes every server-known field. Output is lowercase hex.
func Compute(prevHash string, e audit.Event) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	for _, f := range []string{
		e.ID,
		e.Timestamp,
		e.Actor,
		e.Edge,
		e.Target,
		e.Outcome,
		e.EvidencePacketID,
		e.IdempotencyKey,
	} {
		var lp [4]byte
		binary.BigEndian.PutUint32(lp[:], uint32(len(f)))
		h.Write(lp[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks that a sequence of events (in chain order) forms an unbroken
// chain: the first event links to Genesis, each subsequent event's prevHash
// equals its predecessor's chainHash, and every stored chainHash recomputes.
// It returns the 0-based index of the first broken link, or -1 if intact.
func Verify(events []audit.Event) int {
	prev := Genesis
	for i, e := range events {
		if e.PrevHash != prev {
			return i
		}
		if !strings.EqualFold(Compute(prev, e), e.ChainHash) {
			return i
		}
		prev = e.ChainHash
	}
	return -1
}
