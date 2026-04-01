package ingest

import (
	"strconv"
	"strings"
	"time"

	"urgentry/internal/sqlite"
)

// parseStatsdMetrics parses statsd-format metric lines from a payload.
// Format: metric_name@unit:value|type|#tag1:val1,tag2:val2|T1234567890
// Each line produces one MetricBucket.
func parseStatsdMetrics(projectID string, payload []byte) []*sqlite.MetricBucket {
	lines := strings.Split(string(payload), "\n")
	var buckets []*sqlite.MetricBucket
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		bucket := parseStatsdLine(projectID, line)
		if bucket != nil {
			buckets = append(buckets, bucket)
		}
	}
	return buckets
}

// parseStatsdLine parses a single statsd line into a MetricBucket.
// Format: metric_name@unit:value|type|#tag1:val1,tag2:val2|T1234567890
func parseStatsdLine(projectID, line string) *sqlite.MetricBucket {
	// Split by pipe to get segments: name@unit:value, type, tags, timestamp
	segments := strings.Split(line, "|")
	if len(segments) < 2 {
		return nil
	}

	// Parse first segment: metric_name@unit:value
	nameValuePart := segments[0]
	var name, unit string
	var value float64

	// Split name@unit from value
	colonIdx := strings.LastIndex(nameValuePart, ":")
	if colonIdx < 0 {
		return nil
	}
	nameUnit := nameValuePart[:colonIdx]
	valueStr := nameValuePart[colonIdx+1:]

	// Parse name and optional unit
	if atIdx := strings.Index(nameUnit, "@"); atIdx >= 0 {
		name = nameUnit[:atIdx]
		unit = nameUnit[atIdx+1:]
	} else {
		name = nameUnit
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	// Parse value
	var err error
	value, err = strconv.ParseFloat(strings.TrimSpace(valueStr), 64)
	if err != nil {
		return nil
	}

	// Parse type (second segment)
	metricType := strings.TrimSpace(segments[1])
	switch metricType {
	case "c", "d", "g", "s":
		// valid
	default:
		return nil
	}

	// Parse optional tags and timestamp from remaining segments
	tags := make(map[string]string)
	ts := time.Now().UTC()

	for _, seg := range segments[2:] {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "#") {
			// Tags: #tag1:val1,tag2:val2
			tagStr := seg[1:]
			for _, pair := range strings.Split(tagStr, ",") {
				pair = strings.TrimSpace(pair)
				if pair == "" {
					continue
				}
				if eqIdx := strings.Index(pair, ":"); eqIdx >= 0 {
					tags[pair[:eqIdx]] = pair[eqIdx+1:]
				} else {
					tags[pair] = ""
				}
			}
		} else if strings.HasPrefix(seg, "T") {
			// Timestamp: T1234567890
			if epoch, err := strconv.ParseInt(seg[1:], 10, 64); err == nil && epoch > 0 {
				ts = time.Unix(epoch, 0).UTC()
			}
		}
	}

	return &sqlite.MetricBucket{
		ProjectID: projectID,
		Name:      name,
		Type:      metricType,
		Value:     value,
		Unit:      unit,
		Tags:      tags,
		Timestamp: ts,
	}
}
