package testflight

import "testing"

func TestSelectVersion(t *testing.T) {
	versions := []Version{{Name: "6.27.0"}, {Name: "6.26.0"}}

	latest, err := selectVersion(versions, "")
	if err != nil || latest != "6.27.0" {
		t.Fatalf("latest = %q, %v", latest, err)
	}

	selected, err := selectVersion(versions, " 6.26.0 ")
	if err != nil || selected != "6.26.0" {
		t.Fatalf("selected = %q, %v", selected, err)
	}

	if _, err := selectVersion(versions, "5.0.0"); err == nil {
		t.Fatal("selected an unavailable version")
	}
}

func TestSortBuildsChoosesLatestAvailableThenNewestUpload(t *testing.T) {
	builds := []build{
		{ID: 10, UploadDate: "2026-11-18T12:00:00Z"},
		{ID: 30, UploadDate: "2026-11-19T12:00:00Z"},
		{ID: 20, UploadDate: "2026-11-17T12:00:00Z", Latest: true},
	}

	sortBuilds(builds)

	if builds[0].ID != 20 || builds[1].ID != 30 || builds[2].ID != 10 {
		t.Fatalf("build order = %d, %d, %d", builds[0].ID, builds[1].ID, builds[2].ID)
	}
}
