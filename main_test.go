package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeSB mimics SilverBullet's /.fs API with an in-memory space.
func fakeSB(t *testing.T, files map[string]string) *httptest.Server {
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/.fs")
		name = strings.TrimPrefix(name, "/")
		switch {
		case name == "" && r.Method == "GET":
			var out []map[string]any
			for k := range files {
				out = append(out, map[string]any{"name": k})
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == "GET":
			c, ok := files[name]
			if !ok {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, c)
		case r.Method == "PUT":
			b, _ := io.ReadAll(r.Body)
			files[name] = string(b)
		case r.Method == "DELETE":
			delete(files, name)
		}
	}))
}

func TestCaseGuards(t *testing.T) {
	if _, err := filterPrefix([]string{"Proyectos/Syncerity/a.md"}, "Proyectos/syncerity"); err == nil ||
		!strings.Contains(err.Error(), "differs only by case") {
		t.Errorf("want prefix case error, got %v", err)
	}
	if _, err := filterPrefix([]string{"P/Notas.md", "P/notas.md"}, "P"); err == nil {
		t.Error("want remote collision error")
	}
	got, err := filterPrefix([]string{"P/a.md", "Other/b.md"}, "P")
	if err != nil || len(got) != 1 || got[0] != "a.md" {
		t.Errorf("unexpected: %v %v", got, err)
	}

	// end to end: run must refuse and write nothing
	space := map[string]string{"Proyectos/Syncerity/x.md": "x"}
	srv := fakeSB(t, space)
	defer srv.Close()
	t.Setenv("SB_TOKEN", "tok")
	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{URL: srv.URL, RemotePrefix: "Proyectos/syncerity"})
	os.WriteFile(filepath.Join(dir, configFile), cfg, 0o644)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o644)
	if err := run(dir, false, "local", io.Discard); err == nil {
		t.Fatal("expected error")
	}
	if len(space) != 1 {
		t.Error("must not push when prefix is ambiguous")
	}
}

func TestSync(t *testing.T) {
	space := map[string]string{
		"Proyectos/p/remote-new.md": "r",
		"Proyectos/p/both.md":       "v1",
		"Other/x.md":                "ignored",
	}
	srv := fakeSB(t, space)
	defer srv.Close()
	t.Setenv("SB_TOKEN", "tok")

	dir := t.TempDir()
	cfg, _ := json.Marshal(Config{URL: srv.URL, RemotePrefix: "Proyectos/p"})
	os.WriteFile(filepath.Join(dir, configFile), cfg, 0o644)
	os.WriteFile(filepath.Join(dir, "local-new.md"), []byte("l"), 0o644)
	os.WriteFile(filepath.Join(dir, "both.md"), []byte("v1"), 0o644)

	sync := func(prefer string) string {
		var b strings.Builder
		if err := run(dir, false, prefer, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	sync("local")
	if space["Proyectos/p/local-new.md"] != "l" {
		t.Error("push failed")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "remote-new.md")); string(b) != "r" {
		t.Error("pull failed")
	}
	if _, ok := space["Other/x.md"]; !ok {
		t.Error("touched outside prefix")
	}

	// edit remote only -> pull; deletes never propagate, the other side restores
	space["Proyectos/p/both.md"] = "v2"
	os.Remove(filepath.Join(dir, "local-new.md"))
	delete(space, "Proyectos/p/remote-new.md")
	sync("local")
	if b, _ := os.ReadFile(filepath.Join(dir, "both.md")); string(b) != "v2" {
		t.Error("remote edit not pulled")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "local-new.md")); string(b) != "l" {
		t.Error("local file deleted by sync / not restored")
	}
	if space["Proyectos/p/remote-new.md"] != "r" {
		t.Error("remote file not restored from local")
	}

	// conflict: repo wins with prefer=local, remote wins with prefer=remote
	space["Proyectos/p/both.md"] = "remote-edit"
	os.WriteFile(filepath.Join(dir, "both.md"), []byte("local-edit"), 0o644)
	sync("local")
	if space["Proyectos/p/both.md"] != "local-edit" {
		t.Error("prefer local failed")
	}
	space["Proyectos/p/both.md"] = "remote-edit2"
	os.WriteFile(filepath.Join(dir, "both.md"), []byte("local-edit2"), 0o644)
	sync("remote")
	if b, _ := os.ReadFile(filepath.Join(dir, "both.md")); string(b) != "remote-edit2" {
		t.Error("prefer remote failed")
	}
	if out := sync("local"); strings.Contains(out, "PUSH") || strings.Contains(out, "PULL") {
		t.Errorf("should be clean, got %q", out)
	}
}
