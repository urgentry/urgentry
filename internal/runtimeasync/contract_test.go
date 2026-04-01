package runtimeasync

import "testing"

func TestEveryFamilyHasDeadLetterSubject(t *testing.T) {
	families := []Family{
		FamilyIngest,
		FamilyNormalize,
		FamilyIssues,
		FamilyAlerts,
		FamilyArtifacts,
		FamilyOperations,
		FamilyBridge,
	}
	for _, family := range families {
		if DeadLetterSubjectByFamily[family] == "" {
			t.Fatalf("family %q missing dead-letter subject", family)
		}
	}
}

func TestSubjectsAreAssignedToExactlyOneStream(t *testing.T) {
	seen := map[Subject]Stream{}
	for stream, subjects := range StreamSubjects {
		for _, subject := range subjects {
			if prior, ok := seen[subject]; ok {
				t.Fatalf("subject %q assigned to both %q and %q", subject, prior, stream)
			}
			seen[subject] = stream
		}
	}
	if len(seen) == 0 {
		t.Fatal("expected runtime subjects")
	}
}

func TestDeadLetterSubjectsLiveOnDeadLetterStream(t *testing.T) {
	for family, subject := range DeadLetterSubjectByFamily {
		found := false
		for _, item := range StreamSubjects[StreamDeadLetter] {
			if item == subject {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("family %q dead-letter subject %q is not on %q", family, subject, StreamDeadLetter)
		}
	}
}
