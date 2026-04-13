package selfhostedops

import "testing"

func TestDefaultSLOPackValidate(t *testing.T) {
	if err := DefaultSLOPack().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultSLOPackCoversEveryPlane(t *testing.T) {
	pack := DefaultSLOPack()
	if got, want := len(pack.Planes), 5; got != want {
		t.Fatalf("len(Planes) = %d, want %d", got, want)
	}
	for _, plane := range []ServicePlane{
		ServicePlaneControl,
		ServicePlaneAsync,
		ServicePlaneCache,
		ServicePlaneBlob,
		ServicePlaneTelemetry,
	} {
		item, ok := pack.lookupPlane(plane)
		if !ok {
			t.Fatalf("missing plane %q", plane)
		}
		if len(item.Alerts) == 0 {
			t.Fatalf("plane %q has no alerts", plane)
		}
		if len(item.Dashboard.Widgets) == 0 {
			t.Fatalf("plane %q has no dashboard widgets", plane)
		}
	}
}
