package domain

// IssueStatus represents the status of an issue/group.
type IssueStatus string

const (
	StatusUnresolved      IssueStatus = "unresolved"
	StatusResolved        IssueStatus = "resolved"
	StatusIgnored         IssueStatus = "ignored"
	StatusMerged          IssueStatus = "merged"
	StatusResolvedNextRel IssueStatus = "resolved_next_release"
)

func (s IssueStatus) String() string { return string(s) }

func (s IssueStatus) Valid() bool {
	switch s {
	case StatusUnresolved, StatusResolved, StatusIgnored, StatusMerged, StatusResolvedNextRel:
		return true
	}
	return false
}

// ResolutionSubstatus describes how a resolved issue should be treated.
type ResolutionSubstatus string

const (
	ResolutionSubstatusNone        ResolutionSubstatus = ""
	ResolutionSubstatusNextRelease ResolutionSubstatus = "next_release"
)

// Level represents an event severity level.
type Level string

const (
	LevelFatal   Level = "fatal"
	LevelError   Level = "error"
	LevelWarning Level = "warning"
	LevelInfo    Level = "info"
	LevelDebug   Level = "debug"
)

func (l Level) String() string { return string(l) }
