package store

import "crypto/rand"

// NewToken returns the unguessable segment of a snapshot's /d/{token} URL.
//
// The token is the page's only protection: the link is emailed and sent over
// Telegram, so it leaves the network by design and anyone holding it can read
// the digest until the snapshot is purged. It must therefore be
// cryptographically random, never derived from the recipient, the date or a
// row id.
//
// rand.Text gives at least 128 bits over the RFC 4648 base32 alphabet, which
// needs no escaping in a path segment, and cannot fail — it crashes the
// process rather than returning a token with less entropy than advertised.
func NewToken() string {
	return rand.Text()
}
