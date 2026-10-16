package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterWebServesIndexAssetsAndSPAFallback(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<h1>panel</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	RegisterWeb(mux, directory)
	for _, path := range []string{"/", "/expenses/42"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "panel") {
			t.Fatalf("GET %s: status = %d, body = %q", path, recorder.Code, recorder.Body.String())
		}
	}

	asset := httptest.NewRecorder()
	mux.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || asset.Body.String() != "app" {
		t.Fatalf("asset: status = %d, body = %q", asset.Code, asset.Body.String())
	}

	missingAsset := httptest.NewRecorder()
	mux.ServeHTTP(missingAsset, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if missingAsset.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", missingAsset.Code)
	}
}
