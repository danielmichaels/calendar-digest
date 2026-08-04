package templates

import "strconv"

// itoa is here because templ interpolates strings and an id is an int64.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
