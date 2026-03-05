package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseJumpTarget(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("use ss, mm:ss, or hh:mm:ss format")
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		secs, err := parseTotalSeconds(parts[0])
		if err != nil {
			return 0, err
		}
		return time.Duration(secs) * time.Second, nil
	case 2:
		minPart := normalizeClockField(parts[0])
		secPart := normalizeClockField(parts[1])

		mins, err := parseTotalMinutes(minPart)
		if err != nil {
			return 0, err
		}
		secs, err := parseClockSeconds(secPart)
		if err != nil {
			return 0, err
		}
		return time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second, nil
	case 3:
		hourPart := normalizeClockField(parts[0])
		minPart := normalizeClockField(parts[1])
		secPart := normalizeClockField(parts[2])

		hours, err := parseTotalHours(hourPart)
		if err != nil {
			return 0, err
		}
		mins, err := parseClockMinutes(minPart)
		if err != nil {
			return 0, err
		}
		secs, err := parseClockSeconds(secPart)
		if err != nil {
			return 0, err
		}
		return time.Duration(hours)*time.Hour +
			time.Duration(mins)*time.Minute +
			time.Duration(secs)*time.Second, nil
	default:
		return 0, fmt.Errorf("use ss, mm:ss, or hh:mm:ss format")
	}
}

func normalizeClockField(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	return s
}

func parseTotalSeconds(s string) (int, error) {
	secs, err := parseNonNegativeInt(strings.TrimSpace(s), "seconds")
	if err != nil {
		return 0, err
	}
	return secs, nil
}

func parseTotalMinutes(s string) (int, error) {
	mins, err := parseNonNegativeInt(s, "minutes")
	if err != nil {
		return 0, err
	}
	return mins, nil
}

func parseTotalHours(s string) (int, error) {
	hours, err := parseNonNegativeInt(s, "hours")
	if err != nil {
		return 0, err
	}
	return hours, nil
}

func parseClockMinutes(s string) (int, error) {
	if len(s) > 2 {
		return 0, fmt.Errorf("minutes must be 0-59")
	}

	mins, err := parseNonNegativeInt(s, "minutes")
	if err != nil {
		return 0, err
	}
	if mins > 59 {
		return 0, fmt.Errorf("minutes must be 0-59")
	}
	return mins, nil
}

func parseClockSeconds(s string) (int, error) {
	if len(s) > 2 {
		return 0, fmt.Errorf("seconds must be 0-59")
	}

	secs, err := parseNonNegativeInt(s, "seconds")
	if err != nil {
		return 0, err
	}
	if secs > 59 {
		return 0, fmt.Errorf("seconds must be 0-59")
	}
	return secs, nil
}

func parseNonNegativeInt(s, label string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("%s must be a number", label)
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%s must be a number", label)
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", label)
	}
	return v, nil
}

func formatJumpClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	mm := total / 60
	ss := total % 60
	return fmt.Sprintf("%02d:%02d", mm, ss)
}

func formatJumpPlaceholder(d time.Duration) string {
	return "00:00"
}
