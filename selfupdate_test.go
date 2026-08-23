package godeps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeSelfToPath(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "myapp")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/owner/repo/releases/tag/v2.0.0", http.StatusFound)
			return
		}
		w.Write(createTestTarGz(t, "myapp", "#!/bin/sh\necho upgraded\n"))
	}))
	defer ts.Close()

	oldBase := githubBaseURL
	githubBaseURL = ts.URL
	defer func() { githubBaseURL = oldBase }()

	ctx := context.Background()

	// 1. Successful upgrade from v1.0.0 to v2.0.0
	ver, err := UpgradeSelfToPath(ctx, "owner", "repo", "v1.0.0", destPath)
	if err != nil {
		t.Fatalf("UpgradeSelfToPath failed: %v", err)
	}
	if ver != "2.0.0" {
		t.Errorf("got version %q, want 2.0.0", ver)
	}

	// 2. Already latest version
	_, err = UpgradeSelfToPath(ctx, "owner", "repo", "v2.0.0", destPath)
	if err == nil {
		t.Errorf("expected error when already at latest version")
	}

	// 3. Dev version upgrade succeeds
	_, err = UpgradeSelfToPath(ctx, "owner", "repo", "dev", destPath)
	if err != nil {
		t.Errorf("expected dev version upgrade to succeed, got %v", err)
	}

	// 4. UpgradeSelf wrapper call
	_, _ = UpgradeSelf(ctx, "owner", "repo", "v1.0.0")

	// 5. Error resolving latest tag
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer tsErr.Close()
	githubBaseURL = tsErr.URL
	if _, err := UpgradeSelfToPath(ctx, "owner", "repo", "v1.0.0", destPath); err == nil {
		t.Errorf("expected error when release resolution fails")
	}
}
