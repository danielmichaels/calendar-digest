package deliver

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrTargetConfig means a target's stored config cannot address anything —
// unparseable JSON, or the one field that channel needs left empty.
//
// It is separated from a delivery failure because no number of retries fixes
// it: it is a row in notification_targets that needs editing, not a transport
// having a bad day.
var ErrTargetConfig = errors.New("deliver: target config")

// decodeTarget reads a target's config into cfg and reports a missing address.
//
// addressed is the value the channel cannot send without, and field names it
// for the error — a target that fails its send should say which column to go
// and look at.
func decodeTarget(raw json.RawMessage, cfg any, addressed func() string, field string) error {
	if err := json.Unmarshal(raw, cfg); err != nil {
		return fmt.Errorf("%w: %w", ErrTargetConfig, err)
	}
	if addressed() == "" {
		return fmt.Errorf("%w: %s is empty", ErrTargetConfig, field)
	}
	return nil
}
