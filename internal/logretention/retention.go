package logretention

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DefaultMaximum = 365 * 24 * time.Hour

var ErrInvalid = errors.New("log retention must be positive whole seconds and no greater than the installation maximum")

func ValidateMaximum(maximum time.Duration) error {
	if maximum <= 0 || maximum%time.Second != 0 {
		return ErrInvalid
	}
	return nil
}

func parse(value string) (time.Duration, error) {
	if daysText, ok := strings.CutSuffix(value, "d"); ok {
		days, err := strconv.ParseInt(daysText, 10, 64)
		if err != nil || days <= 0 || days > int64(time.Duration(1<<63-1)/(24*time.Hour)) {
			return 0, ErrInvalid
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return duration, nil
}

func Resolve(value string, maximum time.Duration) (time.Duration, error) {
	if value == "" {
		return maximum, nil
	}
	duration, err := parse(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 || duration%time.Second != 0 || duration > maximum {
		return 0, ErrInvalid
	}
	return duration, nil
}

func Format(duration time.Duration) string {
	if duration%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", duration/(24*time.Hour))
	}
	return duration.String()
}
