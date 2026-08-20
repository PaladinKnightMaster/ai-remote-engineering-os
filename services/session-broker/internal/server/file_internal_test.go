package server

// White-box test for fileBodyLimit's derivation itself (T-139b review finding).
// Deliberately in package server, not server_test: fileBodyLimit is unexported,
// and the property under test — the derived limit must always be able to hold
// the full base64 encoding of a file at the cap — needs to be asserted directly
// against the function, independent of any HTTP round trip or of
// fileEnvelopeHeadroom's size, because the headroom is exactly what let a prior
// version of this test pass under a broken derivation (see below).

import (
	"encoding/base64"
	"testing"
)

// TestFileBodyLimitCoversBase64Expansion is the headroom-independent proof that
// fileBodyLimit's original review missed: an end-to-end HTTP test at a SMALL
// content cap (e.g. 4 KiB) cannot distinguish the correct derivation from a
// mutated one that forgets the base64 expansion factor entirely, because
// fileEnvelopeHeadroom alone is bigger than the whole base64 payload at that
// cap size — the mutation is invisible until the cap is large enough that
// base64 growth (~4/3 of maxContentBytes) exceeds the headroom, which is
// exactly the production case (1 MiB default).
//
// This test instead asserts the property directly, for every n: the limit
// must be at least the exact base64.StdEncoding output length for n raw
// bytes — the same computation encoding/json performs when marshaling the
// []byte content field. There is no headroom to hide behind here; the
// comparison is against the real encoded length, not against a fixed small
// n where slack dominates.
func TestFileBodyLimitCoversBase64Expansion(t *testing.T) {
	for _, n := range []int{0, 1, 4096, 65536, 262144, 1 << 20, 8 << 20} {
		got := fileBodyLimit(n)
		want := int64(base64.StdEncoding.EncodedLen(n))
		if got < want {
			t.Fatalf("fileBodyLimit(%d) = %d, must be >= base64-encoded length %d (a file exactly at this cap would fail to decode)", n, got, want)
		}
	}
}
