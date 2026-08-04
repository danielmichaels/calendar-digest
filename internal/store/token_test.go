package store_test

import (
	"net/url"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/store"
)

// The token is the only thing protecting a detail page, so the floor matters
// more than the exact length. 26 base32 characters is the 128 bits below which
// the guess becomes worth attempting.
func TestNewTokenHasEnoughEntropy(t *testing.T) {
	if got := len(store.NewToken()); got < 26 {
		t.Errorf("token length = %d, want at least 26 characters (128 bits)", got)
	}
}

// The token goes straight into a path segment. Any encoding needing escaping
// would produce links that work until something normalises them.
func TestNewTokenIsSafeInAPathSegment(t *testing.T) {
	tok := store.NewToken()
	if escaped := url.PathEscape(tok); escaped != tok {
		t.Errorf("token %q escapes to %q; the alphabet is not URL-safe", tok, escaped)
	}
}

func TestNewTokenIsDistinctPerCall(t *testing.T) {
	const draws = 1000
	seen := make(map[string]struct{}, draws)
	for range draws {
		tok := store.NewToken()
		if _, dup := seen[tok]; dup {
			t.Fatalf("token %q repeated within %d draws", tok, draws)
		}
		seen[tok] = struct{}{}
	}
}
