package selfhostedops

import "testing"

func TestDefaultRepairPackValidate(t *testing.T) {
	if err := DefaultRepairPack().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultRepairPackCoversReplayProfileAndQuota(t *testing.T) {
	pack := DefaultRepairPack()
	for _, surface := range []RepairSurface{
		RepairSurfaceReplay,
		RepairSurfaceProfile,
		RepairSurfaceQuota,
	} {
		item, ok := pack.lookupSurface(surface)
		if !ok {
			t.Fatalf("missing repair surface %q", surface)
		}
		if len(item.Actions) == 0 {
			t.Fatalf("repair surface %q has no actions", surface)
		}
		if len(item.Safeguards) == 0 {
			t.Fatalf("repair surface %q has no safeguards", surface)
		}
	}
}
