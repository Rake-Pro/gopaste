// Theme discovery and serving. A theme is a CSS file defining a
// [data-theme="name"] token block. The base "rake" theme lives in
// application.css (:root); alternate themes are files under static/themes,
// optionally overlaid by an external config.Theme.Dir served at /themes. The
// resolved list, default, and forced theme are injected into index.html so the
// frontend switcher and first paint stay in sync with server config.
package handler

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rs/zerolog/log"
)

// baseTheme is always present; it is defined on :root in application.css and so
// has no file of its own.
const baseTheme = "rake"

// themeFileRE matches a "<name>.css" basename with a safe theme name. It rejects
// path separators and dot-segments, so an external overlay filename can never
// traverse outside its directory.
var themeFileRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*\.css$`)

// themeState is the resolved theming config shared with the frontend.
type themeState struct {
	names        []string // full switcher list, baseTheme first
	files        []string // themes backed by a CSS file (i.e. everything but baseTheme)
	initial      string   // painted on first load: forced if set, else default
	defaultTheme string
	forcedTheme  string // "" when not forced
	dir          string // external overlay dir, "" when disabled
}

// discoverThemes assembles the theme list from baseTheme, the embedded themes/
// directory, and cfg.Theme.Dir when set. Configured default/forced themes that
// are not in the resulting set are logged and dropped (default -> rake,
// forced -> unforced) so a typo never leaves the UI referencing a missing file.
func discoverThemes(cfg config.Config, assets fs.FS) themeState {
	set := map[string]bool{baseTheme: true}
	fileSet := map[string]bool{}

	collect := func(name string, src string) {
		if name == baseTheme || !cfg.Theme.ValidName(name) {
			if name != baseTheme {
				log.Warn().Str("theme", name).Str("source", src).Msg("skipping theme with unsafe name")
			}
			return
		}
		set[name] = true
		fileSet[name] = true
	}

	if entries, err := fs.ReadDir(assets, "themes"); err == nil {
		for _, e := range entries {
			if n, ok := themeName(e); ok {
				collect(n, "embedded")
			}
		}
	}
	if cfg.Theme.Dir != "" {
		if entries, err := os.ReadDir(cfg.Theme.Dir); err != nil {
			log.Warn().Err(err).Str("dir", cfg.Theme.Dir).Msg("theme dir unreadable; overlay disabled")
		} else {
			for _, e := range entries {
				if n, ok := themeName(e); ok {
					collect(n, "overlay")
				}
			}
		}
	}

	files := make([]string, 0, len(fileSet))
	for n := range fileSet {
		files = append(files, n)
	}
	sort.Strings(files)
	names := append([]string{baseTheme}, files...)

	ts := themeState{names: names, files: files, dir: cfg.Theme.Dir}

	ts.defaultTheme = cfg.Theme.Default
	if !set[ts.defaultTheme] {
		log.Warn().Str("theme", ts.defaultTheme).Msg("theme.default not found; using rake")
		ts.defaultTheme = baseTheme
	}
	if cfg.Theme.Forced != "" {
		if set[cfg.Theme.Forced] {
			ts.forcedTheme = cfg.Theme.Forced
		} else {
			log.Warn().Str("theme", cfg.Theme.Forced).Msg("theme.forced not found; not forcing a theme")
		}
	}
	ts.initial = ts.defaultTheme
	if ts.forcedTheme != "" {
		ts.initial = ts.forcedTheme
	}
	return ts
}

// themeName returns the theme name for a directory entry that is a *.css file.
func themeName(e fs.DirEntry) (string, bool) {
	if e.IsDir() || !themeFileRE.MatchString(e.Name()) {
		return "", false
	}
	return strings.TrimSuffix(e.Name(), ".css"), true
}

// renderIndex injects the resolved theme state into the index.html template.
func renderIndex(indexTmpl []byte, ts themeState) ([]byte, error) {
	t, err := template.New("index").Parse(string(indexTmpl))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{
		"InitialTheme": ts.initial,
		"DefaultTheme": ts.defaultTheme,
		"ForcedTheme":  ts.forcedTheme,
		"ThemesAttr":   strings.Join(ts.names, ","),
		"ThemeFiles":   ts.files,
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// serveThemeOverlay serves /themes/<name>.css from the external overlay dir when
// one is configured and the file exists there. It reports whether it handled the
// request; false means the caller should fall back to the embedded asset.
func (h *Handler) serveThemeOverlay(w http.ResponseWriter, r *http.Request, name string) bool {
	if h.themeDir == "" {
		return false
	}
	base := strings.TrimPrefix(name, "themes/")
	if !themeFileRE.MatchString(base) {
		return false
	}
	full := filepath.Join(h.themeDir, base)
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		return false
	}
	w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(h.cfg.StaticMaxAge))
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	http.ServeFile(w, r, full)
	return true
}
