package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const indexHTML = `<!doctype html><html><body><div id="root"></div><script type="module" src="/assets/index-abc123.js"></script></body></html>`

func builtFS() fstest.MapFS {
	return fstest.MapFS{
		".gitkeep":                {},
		"index.html":              {Data: []byte(indexHTML)},
		"assets/index-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc123.css": {Data: []byte("body{margin:0}")},
		"favicon.svg":             {Data: []byte("<svg/>")},
	}
}

// reply is what a handler answered.
type reply struct {
	status int
	header http.Header
	body   string
}

func get(t *testing.T, h http.Handler, method, target string) reply {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return reply{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

func TestHandlerServesTheAppAndItsRoutes(t *testing.T) {
	h := Handler(builtFS())

	// index.html for the root and for client-side routes at any depth.
	for _, target := range []string{"/", "/index.html", "/login", "/app/instances", "/app/instances/9f1c?tab=usage", "/stage"} {
		r := get(t, h, http.MethodGet, target)
		if r.status != http.StatusOK || r.body != indexHTML {
			t.Errorf("GET %s: status %d, want the index page", target, r.status)
		}
		if got := r.header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", target, got)
		}
		if got := r.header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type %q, want text/html", target, got)
		}
	}
}

func TestHandlerServesAssets(t *testing.T) {
	h := Handler(builtFS())
	for target, wantType := range map[string]string{
		"/assets/index-abc123.js":  "javascript",
		"/assets/index-abc123.css": "text/css",
	} {
		r := get(t, h, http.MethodGet, target)
		if r.status != http.StatusOK {
			t.Fatalf("GET %s: status %d", target, r.status)
		}
		if got := r.header.Get("Content-Type"); !strings.Contains(got, wantType) {
			t.Errorf("GET %s: Content-Type %q, want %q", target, got, wantType)
		}
		if got := r.header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("GET %s: Cache-Control %q, want immutable", target, got)
		}
		if got := r.header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options %q", target, got)
		}
	}

	// Files outside assets/ may change without changing their name.
	if got := get(t, h, http.MethodGet, "/favicon.svg").header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("favicon Cache-Control %q, want no-cache", got)
	}
}

func TestHandlerMissingFilesAre404(t *testing.T) {
	h := Handler(builtFS())
	for _, target := range []string{
		"/assets/index-OLD.js", // a build that is gone: a 200 index page here would be parsed as JavaScript
		"/favicon.ico",
		"/robots.txt",
		"/.gitkeep",
		"/assets/.hidden",
	} {
		if r := get(t, h, http.MethodGet, target); r.status != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", target, r.status)
		}
	}
}

func TestHandlerDoesNotListDirectories(t *testing.T) {
	h := Handler(builtFS())
	if got := get(t, h, http.MethodGet, "/assets/").body; got != indexHTML {
		t.Errorf("GET /assets/ = %q, want the index page, not a listing", got)
	}
}

func TestHandlerCleansPaths(t *testing.T) {
	h := Handler(builtFS())
	// Dot-dot segments resolve inside the build, as for any URL.
	if got := get(t, h, http.MethodGet, "/assets/../favicon.svg").body; got != "<svg/>" {
		t.Errorf("GET /assets/../favicon.svg = %q, want the favicon", got)
	}
	// Going above the root stays at the root: no file there, so an app route.
	for _, target := range []string{"/../../etc/passwd", "//..//etc/passwd"} {
		if got := get(t, h, http.MethodGet, target).body; got != indexHTML {
			t.Errorf("GET %s = %q, want the index page", target, got)
		}
	}
}

func TestHandlerOnlyGetAndHead(t *testing.T) {
	h := Handler(builtFS())
	if r := get(t, h, http.MethodHead, "/login"); r.status != http.StatusOK || r.body != "" {
		t.Errorf("HEAD /login: status %d with body %q, want 200 and none", r.status, r.body)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if r := get(t, h, method, "/login"); r.status != http.StatusNotFound {
			t.Errorf("%s /login: status %d, want 404", method, r.status)
		}
	}
}

func TestHandlerWithoutABuildExplainsItself(t *testing.T) {
	placeholder := fstest.MapFS{".gitkeep": {}}
	if Built(placeholder) {
		t.Fatal("Built is true for a placeholder-only directory")
	}
	h := Handler(placeholder)
	for _, target := range []string{"/", "/index.html", "/app/instances"} {
		r := get(t, h, http.MethodGet, target)
		if r.status != http.StatusServiceUnavailable || !strings.Contains(r.body, "make web") {
			t.Errorf("GET %s: status %d, want 503 saying to run make web", target, r.status)
		}
	}
	// A missing asset is still just missing.
	if r := get(t, h, http.MethodGet, "/assets/x.js"); r.status != http.StatusNotFound {
		t.Errorf("GET /assets/x.js: status %d, want 404", r.status)
	}
}

// TestBuiltApp checks the real embedded build when `make web` has run: the
// index page is served and every asset it names exists. It skips on a checkout
// without a build.
func TestBuiltApp(t *testing.T) {
	fsys := Dist()
	if !Built(fsys) {
		t.Skip("web/dist is not built; run `make web`")
	}
	h := Handler(fsys)
	page := get(t, h, http.MethodGet, "/app/instances")
	if page.status != http.StatusOK || !strings.Contains(page.body, `id="root"`) {
		t.Fatalf("index page: status %d, body %q", page.status, page.body)
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(page.body, -1)
	if len(assets) == 0 {
		t.Fatal("index.html references no assets")
	}
	for _, m := range assets {
		if r := get(t, h, http.MethodGet, m[1]); r.status != http.StatusOK {
			t.Errorf("GET %s: status %d", m[1], r.status)
		}
	}
}
