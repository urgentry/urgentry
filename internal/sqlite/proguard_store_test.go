package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/proguard"
	"urgentry/internal/store"
)

func TestProGuardStore_SaveGetListLookup(t *testing.T) {
	db := openStoreTestDB(t)
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'app', 'App', 'android', 'active')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	blobs := store.NewMemoryBlobStore()
	ps := NewProGuardStore(db, blobs)
	ctx := context.Background()

	mapping := &proguard.Mapping{
		ProjectID: "proj-1",
		ReleaseID: "1.2.3",
		UUID:      "660f839b-8bfd-580d-9a7c-ea339a6c9867",
		CodeID:    "code-123",
		Name:      "proguard.txt",
		Checksum:  "sha1-abc",
		CreatedAt: time.Now().UTC(),
	}
	data := []byte("proguard mapping content")

	if err := ps.SaveMapping(ctx, mapping, data); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}
	if mapping.ID == "" {
		t.Fatal("expected mapping ID to be set")
	}
	if mapping.ObjectKey == "" {
		t.Fatal("expected object key to be set")
	}

	got, payload, err := ps.GetMapping(ctx, mapping.ID)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got == nil {
		t.Fatal("GetMapping returned nil")
	}
	if string(payload) != string(data) {
		t.Fatalf("GetMapping payload = %q, want %q", payload, data)
	}
	if got.UUID != mapping.UUID || got.ReleaseID != mapping.ReleaseID {
		t.Fatalf("GetMapping = %+v, want UUID %q release %q", got, mapping.UUID, mapping.ReleaseID)
	}

	list, err := ps.ListByRelease(ctx, "proj-1", "1.2.3")
	if err != nil {
		t.Fatalf("ListByRelease: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByRelease len = %d, want 1", len(list))
	}

	lookedUp, payload, err := ps.LookupByUUID(ctx, "proj-1", "1.2.3", mapping.UUID)
	if err != nil {
		t.Fatalf("LookupByUUID: %v", err)
	}
	if lookedUp == nil {
		t.Fatal("LookupByUUID returned nil")
	}
	if string(payload) != string(data) {
		t.Fatalf("LookupByUUID payload = %q, want %q", payload, data)
	}

	miss, _, err := ps.LookupByUUID(ctx, "proj-1", "9.9.9", mapping.UUID)
	if err != nil {
		t.Fatalf("LookupByUUID wrong release: %v", err)
	}
	if miss != nil {
		t.Fatal("expected lookup with wrong release to miss")
	}
}
