package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/bilus/gomodproxy/pkg/store"
	"github.com/bilus/gomodproxy/pkg/vcs"
)

type logger = func(v ...interface{})

type api struct {
	log      logger
	gitdir   string
	vcsPaths []vcsPath
	stores   []store.Store
	semc     chan struct{}

	ephemeralTagStorage *vcs.EphemeralTagStorage
}

type vcsPath struct {
	prefix string
	vcs    func(module string) vcs.VCS
}

// Option configures an API handler.
type Option func(*api)

var (
	apiList = regexp.MustCompile(`^/(?P<module>.*)/@v/list$`)
	apiInfo = regexp.MustCompile(`^/(?P<module>.*)/@v/(?P<version>.*).info$`)
	apiMod  = regexp.MustCompile(`^/(?P<module>.*)/@v/(?P<version>.*).mod$`)
	apiZip  = regexp.MustCompile(`^/(?P<module>.*)/@v/(?P<version>.*).zip$`)
	apiTag  = regexp.MustCompile(`^/tags/(?P<module>.*)/@v/(?P<version>.*)$`)
)

var (
	cacheHits            = expvar.NewMap("cache_hits_total")
	cacheMisses          = expvar.NewMap("cache_misses_total")
	httpRequests         = expvar.NewMap("http_requests_total")
	httpErrors           = expvar.NewMap("http_errors_total")
	httpRequestDurations = expvar.NewMap("http_request_duration_seconds")
)

// New returns a configured http.Handler which implements GOPROXY API.
func New(options ...Option) http.Handler {
	api := &api{log: func(...interface{}) {}, semc: make(chan struct{}, 1)}
	for _, opt := range options {
		opt(api)
	}
	return api
}

// Log configures API to use a specific logger function, such as log.Println,
// testing.T.Log or any other custom logger.
func Log(log logger) Option { return func(api *api) { api.log = log } }

// GitDir configures API to use a specific directory for bare git repos.
func GitDir(dir string) Option { return func(api *api) { api.gitdir = dir } }

// Git configures API to use a specific git client when trying to download a
// repository with the given prefix. Auth string can be a path to the SSK key,
// or a colon-separated username:password string.
func Git(prefix, key, password string) Option {
	a := vcs.Key(key, password)
	if creds := strings.SplitN(key, ":", 2); len(creds) == 2 {
		a = vcs.Password(creds[0], creds[1])
	}
	return func(api *api) {
		api.vcsPaths = append(api.vcsPaths, vcsPath{
			prefix: prefix,
			vcs: func(module string) vcs.VCS {
				return vcs.NewGit(api.log, api.gitdir, module, a)
			},
		})
	}
}

// GitWithEphemeralTags configures API to use a specific git client when trying
// to download a repository with the given prefix. Auth string can be a path to
// the SSK key, or a colon-separated username:password string.
func GitWithEphemeralTags(prefix, key, password string) Option {
	// TODO(bilus): Ugly but we don't want to mess with the : encoding so
	// we'll work around the issue of having to pass a passphrase
	// to decrypt a key.
	storage := vcs.NewEphemeralTagStorage()
	a := vcs.Key(key, password)
	if creds := strings.SplitN(key, ":", 2); len(creds) == 2 {
		a = vcs.Password(creds[0], creds[1])
	}
	return func(api *api) {
		api.ephemeralTagStorage = storage
		api.vcsPaths = append(api.vcsPaths, vcsPath{
			prefix: prefix,
			vcs: func(module string) vcs.VCS {
				return vcs.NewGitWithEphemeralTags(api.log, api.gitdir, module, a, storage)
			},
		})
	}
}

func CustomVCS(prefix string, cmd string) Option {
	return func(api *api) {
		api.vcsPaths = append(api.vcsPaths, vcsPath{
			prefix: prefix,
			vcs: func(module string) vcs.VCS {
				return vcs.NewCommand(api.log, cmd, module)
			},
		})
	}
}

// Memory configures API to use in-memory cache for downloaded modules.
func Memory(log logger, limit int64) Option {
	return func(api *api) {
		api.stores = append(api.stores, store.Memory(log, limit))
	}
}

// CacheDir configures API to use a local disk storage for downloaded modules.
func CacheDir(dir string) Option {
	return func(api *api) {
		api.stores = append(api.stores, store.Disk(dir))
	}
}

// VCSWorkers configures API to use at most n parallel workers when fetching
// from the VCS. The reason to restrict number of workers is to limit their
// memory usage.
func VCSWorkers(n int) Option {
	return func(api *api) {
		api.semc = make(chan struct{}, n)
	}
}

func decodeBangs(s string) string {
	buf := []rune{}
	bang := false
	for _, r := range []rune(s) {
		if bang {
			bang = false
			buf = append(buf, unicode.ToUpper(r))
			continue
		}
		if r == '!' {
			bang = true
			continue
		}
		buf = append(buf, r)
	}
	return string(buf)
}

func (api *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	defer func() { api.log("api.ServeHTTP", "method", r.Method, "url", r.URL, "time", time.Since(now)) }()

	for _, route := range []struct {
		id      string
		regexp  *regexp.Regexp
		handler func(w http.ResponseWriter, r *http.Request, module, version string)
	}{
		{"list", apiList, api.list},
		{"info", apiInfo, api.info},
		{"api", apiMod, api.mod},
		{"zip", apiZip, api.zip},
		{"tag", apiTag, api.tag},
	} {
		if m := route.regexp.FindStringSubmatch(r.URL.Path); m != nil {
			module, version := m[1], ""
			if len(m) > 2 {
				version = m[2]
			}
			module = decodeBangs(module)
			if r.Method == http.MethodDelete && version != "" {
				api.delete(w, r, module, version)
				return
			}
			httpRequests.Add(route.id, 1)
			defer func() {
				v := &expvar.Float{}
				v.Set(time.Since(now).Seconds())
				httpRequestDurations.Set(route.id, v)
			}()
			route.handler(w, r, module, version)
			return
		}
	}

	httpRequests.Add("not_found", 1)
	http.NotFound(w, r)
}

func (api *api) vcs(ctx context.Context, module string) vcs.VCS {
	for _, path := range api.vcsPaths {
		if strings.HasPrefix(module, path.prefix) {
			return path.vcs(module)
		}
	}
	return vcs.NewGoMod(api.log, module)
}

func (api *api) module(ctx context.Context, module string, version vcs.Version) ([]byte, time.Time, error) {
	for _, store := range api.stores {
		if snapshot, err := store.Get(ctx, module, version); err == nil {
			cacheHits.Add(module, 1)
			return snapshot.Data, snapshot.Timestamp, nil
		}
	}
	cacheMisses.Add(module, 1)

	// wait for semaphore
	api.semc <- struct{}{}
	defer func() { <-api.semc }()

	timestamp, err := api.vcs(ctx, module).Timestamp(ctx, version)
	if err != nil {
		return nil, time.Time{}, err
	}

	b := &bytes.Buffer{}
	zr, err := api.vcs(ctx, module).Zip(ctx, version)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer zr.Close()

	if _, err := io.Copy(b, zr); err != nil {
		return nil, time.Time{}, err
	}

	for i := len(api.stores) - 1; i >= 0; i-- {
		err := api.stores[i].Put(ctx, store.Snapshot{
			Module:    module,
			Version:   version,
			Timestamp: timestamp,
			Data:      b.Bytes(),
		})
		if err != nil {
			api.log("api.module.Put", "module", module, "version", version, "error", err)
		}
	}

	return b.Bytes(), timestamp, nil
}

func (api *api) list(w http.ResponseWriter, r *http.Request, module, version string) {
	api.log("api.list", "module", module)
	list, err := api.vcs(r.Context(), module).List(r.Context())
	if err != nil {
		api.log("api.list", "module", module, "error", err)
		httpErrors.Add(module, 1)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	for _, v := range list {
		fmt.Fprintln(w, string(v))
	}
}

func (api *api) info(w http.ResponseWriter, r *http.Request, module, version string) {
	api.log("api.info", "module", module, "version", version)
	_, t, err := api.module(r.Context(), module, vcs.Version(version))

	if err != nil {
		api.log("api.info", "module", module, "version", version, "error", err)
		httpErrors.Add(module, 1)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(struct {
		Version string
		Time    time.Time
	}{version, t})
}

func (api *api) mod(w http.ResponseWriter, r *http.Request, module, version string) {
	api.log("api.mod", "module", module, "version", version)
	b, _, err := api.module(r.Context(), module, vcs.Version(version))
	if err == nil {
		if zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b))); err == nil {
			for _, f := range zr.File {
				if f.Name == filepath.Join(module+"@"+string(version), "go.mod") {
					if r, err := f.Open(); err == nil {
						defer r.Close()
						io.Copy(w, r)
						return
					}
				}
			}
		}
	}
	w.Write([]byte(fmt.Sprintf("module %s\n", module)))
}

func (api *api) zip(w http.ResponseWriter, r *http.Request, module, version string) {
	api.log("api.zip", "module", module, "version", version)
	b, _, err := api.module(r.Context(), module, vcs.Version(version))
	if err != nil {
		api.log("api.zip", "module", module, "version", version, "error", err)
		httpErrors.Add(module, 1)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	io.Copy(w, bytes.NewReader(b))
}

func (api *api) delete(w http.ResponseWriter, r *http.Request, module, version string) {
	for _, store := range api.stores {
		if err := store.Del(r.Context(), module, vcs.Version(version)); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	}
}

func (api *api) tag(w http.ResponseWriter, r *http.Request, module, version string) {
	api.log("api.tag", "module", module, "version", version)

	taggable, ok := api.vcs(r.Context(), module).(vcs.Taggable)
	if !ok {
		err := fmt.Errorf("repository for module %v is not taggable", module)
		api.log("api.tag", "module", module, "version", version, "error", err)
		httpErrors.Add(module, 1)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	defer r.Body.Close()

	req := struct {
		Short string `json:"short"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		api.log("api.tag", "module", module, "version", version, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = taggable.Tag(r.Context(), vcs.Version(version), req.Short)
	if err != nil {
		api.log("api.tag", "module", module, "version", version, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}
