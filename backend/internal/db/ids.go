package db

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TimeLayout is the canonical timestamp format stored in TEXT columns
// (SPEC §4.3): ISO-8601 UTC with milliseconds, fixed width so lexical order
// equals chronological order.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// NewID returns a UUIDv7 (time-ordered) as a lowercase 36-character string.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if the random source does; treat as fatal.
		panic(fmt.Errorf("db: uuid v7: %w", err))
	}
	return id.String()
}

// FormatTime renders t in TimeLayout (UTC).
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// Now returns the current time in TimeLayout.
func Now() string { return FormatTime(time.Now()) }

// ParseTime parses a TimeLayout string.
func ParseTime(s string) (time.Time, error) { return time.Parse(TimeLayout, s) }

// Ptr returns a pointer to v (for nullable sqlc params).
func Ptr[T any](v T) *T { return &v }
