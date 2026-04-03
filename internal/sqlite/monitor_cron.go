package sqlite

import (
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	any    bool
	values map[int]bool
}

func computeNextCheckIn(base time.Time, cfg MonitorConfig) time.Time {
	cfg = normalizeMonitorConfig(cfg)
	if base.IsZero() {
		base = time.Now().UTC()
	}
	switch cfg.Schedule.Type {
	case "interval":
		return addInterval(base, cfg.Schedule.Value, cfg.Schedule.Unit)
	case "crontab":
		next, _ := nextCronOccurrence(base, cfg.Schedule.Crontab, cfg.Timezone)
		return next
	default:
		return time.Time{}
	}
}

func advancePast(base time.Time, cfg MonitorConfig, now time.Time) time.Time {
	next := computeNextCheckIn(base, cfg)
	for !next.IsZero() && !next.After(now) {
		next = computeNextCheckIn(next, cfg)
	}
	return next
}

func addInterval(base time.Time, value int, unit string) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "minute", "minutes":
		return base.Add(time.Duration(value) * time.Minute)
	case "hour", "hours":
		return base.Add(time.Duration(value) * time.Hour)
	case "day", "days":
		return base.AddDate(0, 0, value)
	case "week", "weeks":
		return base.AddDate(0, 0, 7*value)
	case "month", "months":
		return base.AddDate(0, value, 0)
	default:
		return time.Time{}
	}
}

func nextCronOccurrence(after time.Time, expr, timezone string) (time.Time, bool) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if err != nil {
		loc = time.UTC
	}
	minute, ok := parseCronField(fields[0], 0, 59)
	if !ok {
		return time.Time{}, false
	}
	hour, ok := parseCronField(fields[1], 0, 23)
	if !ok {
		return time.Time{}, false
	}
	day, ok := parseCronField(fields[2], 1, 31)
	if !ok {
		return time.Time{}, false
	}
	month, ok := parseCronField(fields[3], 1, 12)
	if !ok {
		return time.Time{}, false
	}
	weekday, ok := parseCronField(fields[4], 0, 7)
	if !ok {
		return time.Time{}, false
	}

	current := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := current.AddDate(1, 0, 0)
	for !current.After(limit) {
		if !matchesCronField(month, int(current.Month())) {
			current = current.Add(time.Minute)
			continue
		}
		if !matchesCronField(hour, current.Hour()) || !matchesCronField(minute, current.Minute()) {
			current = current.Add(time.Minute)
			continue
		}
		dayMatch := matchesCronField(day, current.Day())
		weekValue := int(current.Weekday())
		weekMatch := matchesCronField(weekday, weekValue) || (weekValue == 0 && matchesCronField(weekday, 7))
		if !cronDayMatches(day, weekday, dayMatch, weekMatch) {
			current = current.Add(time.Minute)
			continue
		}
		return current.UTC(), true
	}
	return time.Time{}, false
}

func parseCronField(raw string, minVal, maxVal int) (cronField, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return cronField{any: true}, true
	}
	field := cronField{values: make(map[int]bool)}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "*/"):
			step, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || step <= 0 {
				return cronField{}, false
			}
			for value := minVal; value <= maxVal; value += step {
				field.values[value] = true
			}
		default:
			value, err := strconv.Atoi(part)
			if err != nil {
				return cronField{}, false
			}
			if value < minVal || value > maxVal {
				return cronField{}, false
			}
			field.values[value] = true
		}
	}
	return field, len(field.values) > 0
}

func matchesCronField(field cronField, value int) bool {
	if field.any {
		return true
	}
	return field.values[value]
}

func cronDayMatches(day, weekday cronField, dayMatch, weekMatch bool) bool {
	if day.any && weekday.any {
		return true
	}
	if day.any {
		return weekMatch
	}
	if weekday.any {
		return dayMatch
	}
	return dayMatch || weekMatch
}
