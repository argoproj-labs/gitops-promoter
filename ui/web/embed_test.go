package web

import "testing"

func TestDashboardFSContainsBootstrapAssets(t *testing.T) {
	if _, err := DashboardFS.Open("static/index.html"); err != nil {
		t.Fatalf("DashboardFS missing static/index.html: %v", err)
	}
	if _, err := DashboardFS.Open("static/assets/placeholder.txt"); err != nil {
		t.Fatalf("DashboardFS missing static/assets/placeholder.txt: %v", err)
	}
}
