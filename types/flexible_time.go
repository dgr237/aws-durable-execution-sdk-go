package types

import (
	"encoding/json"
	"fmt"
	"time"
)

// FlexibleTime is a time.Time wrapper that can unmarshal from both:
// - Unix timestamp in milliseconds (as a number)
// - RFC3339/ISO-8601 string format
type FlexibleTime struct {
	time.Time
}

// UnmarshalJSON implements json.Unmarshaler interface
func (ft *FlexibleTime) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a number (Unix timestamp in milliseconds)
	if len(data) > 0 && data[0] >= '0' && data[0] <= '9' {
		var timestamp int64
		if err := json.Unmarshal(data, &timestamp); err == nil {
			// Convert milliseconds to time.Time
			ft.Time = time.UnixMilli(timestamp)
			return nil
		}
	}

	// Try to unmarshal as a string (RFC3339 format)
	var timeStr string
	if err := json.Unmarshal(data, &timeStr); err == nil {
		parsedTime, err := time.Parse(time.RFC3339, timeStr)
		if err != nil {
			// Try parsing with nanoseconds
			parsedTime, err = time.Parse(time.RFC3339Nano, timeStr)
			if err != nil {
				return fmt.Errorf("failed to parse time string: %w", err)
			}
		}
		ft.Time = parsedTime
		return nil
	}

	return fmt.Errorf("time value must be either a number (Unix milliseconds) or RFC3339 string, got: %s", string(data))
}

// MarshalJSON implements json.Marshaler interface
func (ft FlexibleTime) MarshalJSON() ([]byte, error) {
	// Marshal as RFC3339 string
	return json.Marshal(ft.Time.Format(time.RFC3339Nano))
}

// ToTimePtr converts FlexibleTime to *time.Time
func (ft *FlexibleTime) ToTimePtr() *time.Time {
	if ft == nil {
		return nil
	}
	return &ft.Time
}

// NewFlexibleTime creates a FlexibleTime from time.Time
func NewFlexibleTime(t time.Time) FlexibleTime {
	return FlexibleTime{Time: t}
}

// NewFlexibleTimePtr creates a *FlexibleTime from *time.Time
func NewFlexibleTimePtr(t *time.Time) *FlexibleTime {
	if t == nil {
		return nil
	}
	return &FlexibleTime{Time: *t}
}
