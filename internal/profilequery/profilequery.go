package profilequery

import (
	"fmt"
	"sort"
	"strings"

	"urgentry/internal/store"
)

type TreeFrame struct {
	ID   string
	Name string
}

type StackAggregate struct {
	Weight      int
	SampleCount int
	Frames      []TreeFrame
}

type treeAccumulator struct {
	Name            string
	FrameID         string
	InclusiveWeight int
	SelfWeight      int
	SampleCount     int
	Children        map[string]*treeAccumulator
}

func BuildTree(profileID, threadID, mode string, stacks []StackAggregate, filter store.ProfileQueryFilter) *store.ProfileTree {
	maxDepth := filter.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 64
	}
	if maxDepth > 256 {
		maxDepth = 256
	}
	maxNodes := filter.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 512
	}
	if maxNodes > 2048 {
		maxNodes = 2048
	}
	frameFilter := strings.ToLower(strings.TrimSpace(filter.FrameFilter))
	root := &treeAccumulator{Name: "root", Children: map[string]*treeAccumulator{}}
	nodeCount := 0
	totalWeight := 0
	totalSamples := 0
	truncated := false
	for _, stack := range stacks {
		frames := stack.Frames
		if frameFilter != "" && !stackMatchesFrameFilter(frames, frameFilter) {
			continue
		}
		if mode != "bottom_up" {
			frames = reverseFrames(frames)
		}
		if len(frames) > maxDepth {
			frames = frames[:maxDepth]
			truncated = true
		}
		totalWeight += stack.Weight
		totalSamples += stack.SampleCount
		insertTreePath(root, frames, stack.Weight, stack.SampleCount, mode == "bottom_up", maxNodes, &nodeCount, &truncated)
	}
	root.InclusiveWeight = totalWeight
	root.SampleCount = totalSamples
	return &store.ProfileTree{
		ProfileID:    profileID,
		ThreadID:     threadID,
		Mode:         mode,
		TotalWeight:  totalWeight,
		TotalSamples: totalSamples,
		Truncated:    truncated,
		Root:         serializeTree(root),
	}
}

func BuildHotPath(tree *store.ProfileTree) *store.ProfileHotPath {
	if tree == nil {
		return nil
	}
	totalWeight := maxInt(1, tree.TotalWeight)
	frames := make([]store.ProfileHotPathFrame, 0, len(tree.Root.Children))
	node := tree.Root
	for len(node.Children) > 0 {
		next := node.Children[0]
		for _, candidate := range node.Children[1:] {
			if candidate.InclusiveWeight > next.InclusiveWeight || (candidate.InclusiveWeight == next.InclusiveWeight && candidate.Name < next.Name) {
				next = candidate
			}
		}
		frames = append(frames, store.ProfileHotPathFrame{
			Name:            next.Name,
			FrameID:         next.FrameID,
			InclusiveWeight: next.InclusiveWeight,
			SampleCount:     next.SampleCount,
			Percent:         (float64(next.InclusiveWeight) / float64(totalWeight)) * 100,
		})
		node = next
	}
	return &store.ProfileHotPath{
		ProfileID:    tree.ProfileID,
		ThreadID:     tree.ThreadID,
		TotalWeight:  tree.TotalWeight,
		TotalSamples: tree.TotalSamples,
		Truncated:    tree.Truncated,
		Frames:       frames,
	}
}

func BuildComparison(baseline, candidate *store.ProfileManifest, threadID string, baselineWeights, candidateWeights map[string]int, baselineDurationNS, candidateDurationNS int64, baselineSampleCount, candidateSampleCount int, filter store.ProfileComparisonFilter) *store.ProfileComparison {
	maxFunctions := filter.MaxFunctions
	if maxFunctions <= 0 {
		maxFunctions = 10
	}
	if maxFunctions > 100 {
		maxFunctions = 100
	}
	allNames := map[string]struct{}{}
	sharedCount := 0
	for name := range baselineWeights {
		allNames[name] = struct{}{}
		if _, ok := candidateWeights[name]; ok {
			sharedCount++
		}
	}
	for name := range candidateWeights {
		allNames[name] = struct{}{}
	}
	deltas := make([]store.ProfileComparisonDelta, 0, len(allNames))
	for name := range allNames {
		deltas = append(deltas, store.ProfileComparisonDelta{
			Name:            name,
			BaselineWeight:  baselineWeights[name],
			CandidateWeight: candidateWeights[name],
			DeltaWeight:     candidateWeights[name] - baselineWeights[name],
		})
	}
	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].DeltaWeight == deltas[j].DeltaWeight {
			return deltas[i].Name < deltas[j].Name
		}
		return deltas[i].DeltaWeight > deltas[j].DeltaWeight
	})
	regressions := make([]store.ProfileComparisonDelta, 0, maxFunctions)
	improvements := make([]store.ProfileComparisonDelta, 0, maxFunctions)
	for _, item := range deltas {
		if item.DeltaWeight > 0 && len(regressions) < maxFunctions {
			regressions = append(regressions, item)
		}
		if item.DeltaWeight < 0 && len(improvements) < maxFunctions {
			improvements = append(improvements, item)
		}
	}
	sort.Slice(improvements, func(i, j int) bool {
		if improvements[i].DeltaWeight == improvements[j].DeltaWeight {
			return improvements[i].Name < improvements[j].Name
		}
		return improvements[i].DeltaWeight < improvements[j].DeltaWeight
	})
	confidence, notes := comparisonConfidence(sharedCount, len(allNames))
	confidence, notes = applyScopeNotes(confidence, notes, baseline, candidate)
	return &store.ProfileComparison{
		BaselineProfileID:    manifestProfileID(baseline),
		CandidateProfileID:   manifestProfileID(candidate),
		ThreadID:             threadID,
		DurationDeltaNS:      candidateDurationNS - baselineDurationNS,
		SampleCountDelta:     candidateSampleCount - baselineSampleCount,
		Confidence:           confidence,
		Notes:                notes,
		TopRegressions:       regressions,
		TopImprovements:      improvements,
		SharedFunctionLabels: sharedCount,
		TotalFunctionLabels:  len(allNames),
	}
}

func EnforceGuard(manifest *store.ProfileManifest, maxNodes int) error {
	if manifest == nil || manifest.ProfileID == "" {
		return store.ErrNotFound
	}
	if manifest.ProcessingStatus != store.ProfileProcessingStatusCompleted {
		return fmt.Errorf("profile %s is not query-ready", manifest.ProfileID)
	}
	if manifest.SampleCount > 250000 || manifest.StackCount > 50000 {
		return store.ErrQueryTooLarge
	}
	if maxNodes > 2048 {
		return store.ErrQueryTooLarge
	}
	return nil
}

func ResolveThread(record *store.ProfileRecord, threadID string) (string, string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", "", nil
	}
	for _, thread := range record.Threads {
		if thread.ThreadKey == threadID || thread.ThreadName == threadID {
			return thread.ID, thread.ThreadKey, nil
		}
	}
	return "", "", store.ErrNotFound
}

func LoadScopeTotals(record *store.ProfileRecord, threadRowID string) (int64, int) {
	if strings.TrimSpace(threadRowID) == "" {
		return record.Manifest.DurationNS, record.Manifest.SampleCount
	}
	for _, thread := range record.Threads {
		if thread.ID == threadRowID {
			return thread.DurationNS, thread.SampleCount
		}
	}
	return 0, 0
}

func LoadStackAggregates(record *store.ProfileRecord, threadRowID string) []StackAggregate {
	framesByID := make(map[string]store.ProfileFrame, len(record.Frames))
	for _, frame := range record.Frames {
		framesByID[frame.ID] = frame
	}
	stackFrames := make(map[string][]store.ProfileStackFrame, len(record.Stacks))
	for _, item := range record.StackFrames {
		stackFrames[item.StackID] = append(stackFrames[item.StackID], item)
	}
	aggregates := map[string]*StackAggregate{}
	var order []string
	for _, sample := range record.Samples {
		if threadRowID != "" && sample.ThreadRowID != threadRowID {
			continue
		}
		item := aggregates[sample.StackID]
		if item == nil {
			frames := append([]store.ProfileStackFrame(nil), stackFrames[sample.StackID]...)
			sort.Slice(frames, func(i, j int) bool { return frames[i].Position < frames[j].Position })
			resolved := make([]TreeFrame, 0, len(frames))
			for _, stackFrame := range frames {
				frame := framesByID[stackFrame.FrameID]
				resolved = append(resolved, TreeFrame{ID: frame.ID, Name: frame.FrameLabel})
			}
			item = &StackAggregate{Frames: resolved}
			aggregates[sample.StackID] = item
			order = append(order, sample.StackID)
		}
		item.Weight += sample.Weight
		item.SampleCount++
	}
	result := make([]StackAggregate, 0, len(order))
	for _, stackID := range order {
		result = append(result, *aggregates[stackID])
	}
	return result
}

func LoadFunctionWeights(record *store.ProfileRecord, threadRowID string) map[string]int {
	framesByID := make(map[string]store.ProfileFrame, len(record.Frames))
	for _, frame := range record.Frames {
		framesByID[frame.ID] = frame
	}
	stackFrames := make(map[string][]string, len(record.Stacks))
	for _, item := range record.StackFrames {
		stackFrames[item.StackID] = append(stackFrames[item.StackID], item.FrameID)
	}
	result := map[string]int{}
	for _, sample := range record.Samples {
		if threadRowID != "" && sample.ThreadRowID != threadRowID {
			continue
		}
		for _, frameID := range stackFrames[sample.StackID] {
			if label := strings.TrimSpace(framesByID[frameID].FunctionLabel); label != "" {
				result[label] += sample.Weight
			}
		}
	}
	return result
}

func Breakdowns(record *store.ProfileRecord, frameLabels bool, limit int) []store.ProfileBreakdown {
	if limit <= 0 {
		limit = 10
	}
	framesByID := make(map[string]store.ProfileFrame, len(record.Frames))
	for _, frame := range record.Frames {
		framesByID[frame.ID] = frame
	}
	stacksByID := make(map[string]store.ProfileStack, len(record.Stacks))
	for _, stack := range record.Stacks {
		stacksByID[stack.ID] = stack
	}
	counts := map[string]int{}
	for _, sample := range record.Samples {
		stack := stacksByID[sample.StackID]
		frame := framesByID[stack.LeafFrameID]
		name := strings.TrimSpace(frame.FunctionLabel)
		if frameLabels {
			name = strings.TrimSpace(frame.FrameLabel)
		}
		if name == "" {
			continue
		}
		counts[name] += sample.Weight
	}
	items := make([]store.ProfileBreakdown, 0, len(counts))
	for name, count := range counts {
		items = append(items, store.ProfileBreakdown{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func insertTreePath(root *treeAccumulator, frames []TreeFrame, weight, sampleCount int, bottomUp bool, maxNodes int, nodeCount *int, truncated *bool) {
	if len(frames) == 0 {
		return
	}
	root.InclusiveWeight += weight
	root.SampleCount += sampleCount
	selfIndex := len(frames) - 1
	if bottomUp {
		selfIndex = 0
	}
	cursor := root
	for i, frame := range frames {
		key := frame.ID + "|" + frame.Name
		child := cursor.Children[key]
		if child == nil {
			if maxNodes > 0 && *nodeCount >= maxNodes {
				*truncated = true
				return
			}
			child = &treeAccumulator{Name: frame.Name, FrameID: frame.ID, Children: map[string]*treeAccumulator{}}
			cursor.Children[key] = child
			*nodeCount++
		}
		child.InclusiveWeight += weight
		child.SampleCount += sampleCount
		if i == selfIndex {
			child.SelfWeight += weight
		}
		cursor = child
	}
}

func serializeTree(node *treeAccumulator) store.ProfileTreeNode {
	if node == nil {
		return store.ProfileTreeNode{}
	}
	children := make([]store.ProfileTreeNode, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, serializeTree(child))
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].InclusiveWeight == children[j].InclusiveWeight {
			return children[i].Name < children[j].Name
		}
		return children[i].InclusiveWeight > children[j].InclusiveWeight
	})
	return store.ProfileTreeNode{
		Name:            node.Name,
		FrameID:         node.FrameID,
		InclusiveWeight: node.InclusiveWeight,
		SelfWeight:      node.SelfWeight,
		SampleCount:     node.SampleCount,
		Children:        children,
	}
}

func reverseFrames(items []TreeFrame) []TreeFrame {
	reversed := make([]TreeFrame, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		reversed = append(reversed, items[i])
	}
	return reversed
}

func stackMatchesFrameFilter(frames []TreeFrame, filter string) bool {
	for _, frame := range frames {
		if strings.Contains(strings.ToLower(frame.Name), filter) {
			return true
		}
	}
	return false
}

func comparisonConfidence(shared, total int) (string, []string) {
	if total == 0 {
		return "empty", []string{"no comparable function labels were found"}
	}
	ratio := float64(shared) / float64(total)
	switch {
	case ratio >= 0.75:
		return "high", nil
	case ratio >= 0.4:
		return "partial", []string{"function-label overlap is partial; comparison results may omit unmatched paths"}
	default:
		return "low", []string{"function-label overlap is low; comparison is limited to the shared subset"}
	}
}

func applyScopeNotes(confidence string, notes []string, baseline, candidate *store.ProfileManifest) (string, []string) {
	notes = append([]string{}, notes...)
	if baseline == nil || candidate == nil {
		return confidence, notes
	}
	if baseline.Transaction != "" && candidate.Transaction != "" && baseline.Transaction != candidate.Transaction {
		notes = append(notes, "profiles have different transactions; comparison is limited and confidence is capped")
		confidence = capConfidence(confidence, "low")
	}
	if baseline.Platform != "" && candidate.Platform != "" && baseline.Platform != candidate.Platform {
		notes = append(notes, "profiles have different platforms; comparison is limited and confidence is capped")
		confidence = capConfidence(confidence, "low")
	}
	if baseline.Environment != "" && candidate.Environment != "" && baseline.Environment != candidate.Environment {
		notes = append(notes, "profiles have different environments; comparison is limited and confidence is capped")
		confidence = capConfidence(confidence, "partial")
	}
	return confidence, notes
}

func capConfidence(current, maximum string) string {
	order := map[string]int{"empty": 0, "low": 1, "partial": 2, "high": 3}
	if order[current] > order[maximum] {
		return maximum
	}
	return current
}

func manifestProfileID(manifest *store.ProfileManifest) string {
	if manifest == nil {
		return ""
	}
	return manifest.ProfileID
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
