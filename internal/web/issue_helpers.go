package web

// ---------------------------------------------------------------------------
// Event Chart Data (Feature 1)
// ---------------------------------------------------------------------------

// chartPoint represents one bar in the event frequency chart.
type chartPoint struct {
	Day    string
	Count  int
	Height int // percentage of max (0-100)
}

// ---------------------------------------------------------------------------
// Tag Distribution (Feature 2)
// ---------------------------------------------------------------------------

// tagDistRow represents the top value for a given tag key with its percentage.
type tagDistRow struct {
	Key     string
	Value   string
	Percent int
	Hue     int // CSS hue for the bar color
}

// ---------------------------------------------------------------------------
// Frame Grouping (Feature 3)
// ---------------------------------------------------------------------------

// frameGroup groups consecutive frames for collapsible display.
type frameGroup struct {
	IsCollapsed bool
	Count       int
	Frames      []stackFrame
}

// groupFrames groups consecutive non-in-app frames into collapsible groups.
func groupFrames(frames []stackFrame) []frameGroup {
	if len(frames) == 0 {
		return nil
	}

	var groups []frameGroup
	var currentLibFrames []stackFrame

	flushLib := func() {
		if len(currentLibFrames) > 0 {
			groups = append(groups, frameGroup{
				IsCollapsed: true,
				Count:       len(currentLibFrames),
				Frames:      currentLibFrames,
			})
			currentLibFrames = nil
		}
	}

	for _, f := range frames {
		if f.InApp {
			flushLib()
			groups = append(groups, frameGroup{
				IsCollapsed: false,
				Count:       1,
				Frames:      []stackFrame{f},
			})
		} else {
			currentLibFrames = append(currentLibFrames, f)
		}
	}
	flushLib()

	return groups
}
