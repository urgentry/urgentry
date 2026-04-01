package issue

import (
	"context"
	"errors"
	"sort"
	"sync"

	"urgentry/internal/domain"
	"urgentry/internal/store"
)

// MemoryGroupStore is a thread-safe in-memory GroupStore for testing and development.
type MemoryGroupStore struct {
	mu     sync.Mutex
	groups []*Group
	byID   map[string]int // id -> slice index
	byKey  map[string]int // "projectID:version:key" -> slice index
}

// NewMemoryGroupStore creates a new in-memory group store.
func NewMemoryGroupStore() *MemoryGroupStore {
	return &MemoryGroupStore{
		byID:  make(map[string]int),
		byKey: make(map[string]int),
	}
}

func groupKey(projectID, version, key string) string {
	return projectID + ":" + version + ":" + key
}

func (m *MemoryGroupStore) UpsertGroup(_ context.Context, g *Group) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	compositeKey := groupKey(g.ProjectID, g.GroupingVersion, g.GroupingKey)

	if idx, ok := m.byKey[compositeKey]; ok {
		existing := m.groups[idx]
		existing.TimesSeen++
		existing.LastEventID = g.LastEventID
		if g.LastSeen.After(existing.LastSeen) {
			existing.LastSeen = g.LastSeen
		}
		// Update title/culprit/level from latest event
		if g.Title != "" {
			existing.Title = g.Title
		}
		if g.Culprit != "" {
			existing.Culprit = g.Culprit
		}
		if g.Level != "" {
			existing.Level = g.Level
		}
		if g.Assignee != "" {
			existing.Assignee = g.Assignee
		}
		// Copy updated state back to caller so it can see the ID and final TimesSeen
		*g = *existing
		return nil
	}

	// New group
	if g.TimesSeen == 0 {
		g.TimesSeen = 1
	}
	if g.Status == "" {
		g.Status = string(domain.StatusUnresolved)
	}

	cp := *g
	idx := len(m.groups)
	m.groups = append(m.groups, &cp)
	m.byID[cp.ID] = idx
	m.byKey[compositeKey] = idx

	// Reflect any defaults back to caller
	*g = cp
	return nil
}

func (m *MemoryGroupStore) GetGroup(_ context.Context, id string) (*Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, ok := m.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *m.groups[idx]
	return &cp, nil
}

func (m *MemoryGroupStore) GetGroupByKey(_ context.Context, projectID, version, key string) (*Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	compositeKey := groupKey(projectID, version, key)
	idx, ok := m.byKey[compositeKey]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *m.groups[idx]
	return &cp, nil
}

func (m *MemoryGroupStore) ListGroups(_ context.Context, projectID string, opts ListOpts) ([]*Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var filtered []*Group
	for _, g := range m.groups {
		if g.ProjectID != projectID {
			continue
		}
		if opts.Status != "" && g.Status != opts.Status {
			continue
		}
		filtered = append(filtered, g)
	}

	// Sort
	switch opts.Sort {
	case "last_seen_asc":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].LastSeen.Before(filtered[j].LastSeen)
		})
	case "first_seen_desc":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].FirstSeen.After(filtered[j].FirstSeen)
		})
	case "first_seen_asc":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].FirstSeen.Before(filtered[j].FirstSeen)
		})
	case "times_seen_desc":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].TimesSeen > filtered[j].TimesSeen
		})
	default: // "last_seen_desc"
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].LastSeen.After(filtered[j].LastSeen)
		})
	}

	// Cursor (cursor is a group ID; skip everything up to and including it)
	if opts.Cursor != "" {
		cursorIdx := -1
		for i, g := range filtered {
			if g.ID == opts.Cursor {
				cursorIdx = i
				break
			}
		}
		if cursorIdx >= 0 && cursorIdx+1 < len(filtered) {
			filtered = filtered[cursorIdx+1:]
		} else if cursorIdx >= 0 {
			filtered = nil
		}
	}

	// Limit
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// Return copies
	result := make([]*Group, len(filtered))
	for i, g := range filtered {
		cp := *g
		result[i] = &cp
	}
	return result, nil
}

func (m *MemoryGroupStore) UpdateStatus(_ context.Context, id string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, ok := m.byID[id]
	if !ok {
		return store.ErrNotFound
	}

	switch domain.IssueStatus(status) {
	case domain.StatusUnresolved, domain.StatusResolved, domain.StatusIgnored, domain.StatusMerged, domain.StatusResolvedNextRel:
		// valid
	default:
		return errors.New("invalid status: " + status)
	}

	m.groups[idx].Status = status
	return nil
}

func (m *MemoryGroupStore) UpdateAssignee(_ context.Context, id string, assignee string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, ok := m.byID[id]
	if !ok {
		return store.ErrNotFound
	}
	m.groups[idx].Assignee = assignee
	return nil
}
