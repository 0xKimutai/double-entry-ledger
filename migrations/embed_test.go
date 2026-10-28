package migrations

import "testing"

func TestLatestVersion(t *testing.T) {
	version, err := LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("latest migration version = %d, want 2", version)
	}
}
