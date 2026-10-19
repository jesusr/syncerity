// sbsync: bidirectional sync between local markdown files and a SilverBullet space.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	configFile = ".sbsync.json"
	stateFile  = ".sbsync-state.json"
)

// Config lives in the repo root as .sbsync.json.
type Config struct {
	URL          string   `json:"url"`          // e.g. https://silverbullet.example.com
	RemotePrefix string   `json:"remotePrefix"` // folder inside the space, e.g. Proyectos/syncerity
	LocalDir     string   `json:"localDir"`     // markdown root, relative to config file (default ".")
	Exclude      []string `json:"exclude"`      // directory names to skip
}

// State maps relative path -> hash of the content at last successful sync.
type State map[string]string

type Remote struct {
	base   string
	prefix string
	token  string
	http   *http.Client
}

type remoteEntry struct {
	Name string `json:"name"`
}

func (r *Remote) req(method, name string, body []byte) (*http.Response, error) {
	u := strings.TrimRight(r.base, "/") + "/.fs"
	if name != "" {
		u += "/" + escapePath(name)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	rq, err := http.NewRequest(method, u, rd)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		rq.Header.Set("Authorization", "Bearer "+r.token)
	}
	rq.Header.Set("X-Sync-Mode", "true")
	rq.Header.Set("Accept", "application/json")
	if body != nil {
		rq.Header.Set("Content-Type", "text/markdown")
	}
	return r.http.Do(rq)
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (r *Remote) full(rel string) string { return path.Join(r.prefix, rel) }

// list returns rel paths (under prefix) of remote .md files.
func (r *Remote) list() ([]string, error) {
	resp, err := r.req("GET", "", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list: %s", resp.Status)
	}
	var entries []remoteEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("list: decode: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name, ".md") {
			names = append(names, e.Name)
		}
	}
	return filterPrefix(names, r.prefix)
}

// filterPrefix returns rel paths under prefix. It fails if the space holds a
// folder matching prefix only when case is ignored (likely a different spelling
// of the same project), or if two rel paths differ only by case.
func filterPrefix(names []string, prefix string) ([]string, error) {
	pre := strings.Trim(prefix, "/")
	if pre != "" {
		pre += "/"
	}
	var out []string
	lookalikes := map[string]bool{}
	for _, n := range names {
		switch {
		case strings.HasPrefix(n, pre):
			out = append(out, strings.TrimPrefix(n, pre))
		case strings.HasPrefix(strings.ToLower(n), strings.ToLower(pre)):
			lookalikes[n[:len(pre)]] = true
		}
	}
	if len(lookalikes) > 0 {
		var l []string
		for k := range lookalikes {
			l = append(l, strings.TrimSuffix(k, "/"))
		}
		sort.Strings(l)
		return nil, fmt.Errorf("remotePrefix %q differs only by case from existing folder(s) %q; fix remotePrefix or rename the folder",
			strings.TrimSuffix(pre, "/"), l)
	}
	return out, checkCaseCollisions("remote", out)
}

// checkCaseCollisions fails when paths differ only by case; they would
// overwrite each other on a case-insensitive filesystem.
func checkCaseCollisions(side string, paths []string) error {
	seen := map[string]string{}
	for _, p := range paths {
		k := strings.ToLower(p)
		if o, ok := seen[k]; ok && o != p {
			return fmt.Errorf("%s: %q and %q differ only by case", side, o, p)
		}
		seen[k] = p
	}
	return nil
}

// get returns content, or nil if the file does not exist.
func (r *Remote) get(rel string) ([]byte, error) {
	resp, err := r.req("GET", r.full(rel), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("get %s: %s", rel, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if b == nil {
		b = []byte{}
	}
	return b, err
}

func (r *Remote) put(rel string, data []byte) error {
	resp, err := r.req("PUT", r.full(rel), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("put %s: %s", rel, resp.Status)
	}
	return nil
}

func hash(b []byte) string {
	if b == nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func loadConfig(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (run `sbsync init`)", configFile, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.URL == "" || c.RemotePrefix == "" {
		return nil, errors.New("config: url and remotePrefix are required")
	}
	if c.LocalDir == "" {
		c.LocalDir = "."
	}
	return &c, nil
}

func loadState(dir string) State {
	s := State{}
	if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveState(dir string, s State) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, stateFile), append(b, '\n'), 0o644)
}

// scanLocal returns rel path -> content for all .md files under root.
func scanLocal(root string, exclude []string) (map[string][]byte, error) {
	skip := map[string]bool{".git": true, "node_modules": true}
	for _, e := range exclude {
		skip[e] = true
	}
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	return out, err
}

// plan decides per file what to do, given local/remote/base hashes ("" = absent).
// It never deletes: a file missing on one side is copied from the other, so the
// result is the union of both sides.
func plan(l, r, b string) string {
	switch {
	case l == r:
		return "" // identical (or both absent)
	case l == "":
		return "pull"
	case r == "":
		return "push"
	case l == b:
		return "pull"
	case r == b:
		return "push"
	default:
		return "conflict"
	}
}

func run(dir string, dry bool, prefer string, out io.Writer) error {
	cfg, err := loadConfig(dir)
	if err != nil {
		return err
	}
	root := filepath.Join(dir, cfg.LocalDir)
	rem := &Remote{
		base:   cfg.URL,
		prefix: cfg.RemotePrefix,
		token:  os.Getenv("SB_TOKEN"),
		http:   &http.Client{Timeout: 30 * time.Second},
	}

	local, err := scanLocal(root, cfg.Exclude)
	if err != nil {
		return err
	}
	lkeys := make([]string, 0, len(local))
	for k := range local {
		lkeys = append(lkeys, k)
	}
	sort.Strings(lkeys)
	if err := checkCaseCollisions("local", lkeys); err != nil {
		return err
	}
	names, err := rem.list()
	if err != nil {
		return err
	}
	remote := map[string][]byte{}
	for _, n := range names {
		b, err := rem.get(n)
		if err != nil {
			return err
		}
		if b != nil {
			remote[n] = b
		}
	}
	state := loadState(dir)

	all := map[string]bool{}
	for k := range local {
		all[k] = true
	}
	for k := range remote {
		all[k] = true
	}
	for k := range state {
		all[k] = true
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pushes, pulls := 0, 0
	for _, rel := range keys {
		lh, rh := hash(local[rel]), hash(remote[rel])
		kind := plan(lh, rh, state[rel])
		if kind == "" {
			setState(state, rel, lh)
			continue
		}
		if kind == "conflict" {
			// changed on both sides: the preferred side wins
			kind = map[string]string{"local": "push", "remote": "pull"}[prefer]
			fmt.Fprintf(out, "CONFLICT      %s (prefer %s)\n", rel, prefer)
		}
		fmt.Fprintf(out, "%-13s %s\n", strings.ToUpper(kind), rel)
		if kind == "push" {
			pushes++
		} else {
			pulls++
		}
		if dry {
			continue
		}
		lp := filepath.Join(root, filepath.FromSlash(rel))
		switch kind {
		case "push":
			err = rem.put(rel, local[rel])
			setState(state, rel, lh)
		case "pull":
			if err = os.MkdirAll(filepath.Dir(lp), 0o755); err == nil {
				err = os.WriteFile(lp, remote[rel], 0o644)
			}
			setState(state, rel, rh)
		}
		if err != nil {
			_ = saveState(dir, state)
			return err
		}
	}
	fmt.Fprintf(out, "%d local, %d remote (%s), %d to push, %d to pull\n",
		len(local), len(remote), cfg.RemotePrefix, pushes, pulls)
	if !dry {
		return saveState(dir, state)
	}
	return nil
}

func setState(s State, rel, h string) {
	if h == "" {
		delete(s, rel)
	} else {
		s[rel] = h
	}
}

func initConfig(dir string) error {
	p := filepath.Join(dir, configFile)
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s already exists", configFile)
	}
	c := Config{
		URL:          "https://silverbullet.example.com",
		RemotePrefix: "Proyectos/" + filepath.Base(mustAbs(dir)),
		LocalDir:     ".",
		Exclude:      []string{},
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func main() {
	fl := flag.NewFlagSet("sbsync", flag.ExitOnError)
	dir := fl.String("C", ".", "project directory (holds "+configFile+")")
	prefer := fl.String("prefer", "local", "who wins when both sides changed: local (repo) | remote (SilverBullet)")
	fl.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: sbsync [flags] init|status|sync\n  token via env SB_TOKEN\n")
		fl.PrintDefaults()
	}
	if len(os.Args) < 2 {
		fl.Usage()
		os.Exit(2)
	}
	// subcommand first or last: sbsync sync -C dir  /  sbsync -C dir sync
	var cmd string
	args := os.Args[1:]
	if !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	_ = fl.Parse(args)
	if cmd == "" && fl.NArg() > 0 {
		cmd = fl.Arg(0)
	}
	if *prefer != "local" && *prefer != "remote" {
		fmt.Fprintln(os.Stderr, "-prefer must be local or remote")
		os.Exit(2)
	}

	var err error
	switch cmd {
	case "init":
		err = initConfig(*dir)
	case "status":
		err = run(*dir, true, *prefer, os.Stdout)
	case "sync":
		err = run(*dir, false, *prefer, os.Stdout)
	default:
		fl.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
