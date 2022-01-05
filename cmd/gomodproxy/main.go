package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/bilus/gomodproxy/pkg/api"

	"expvar"
	_ "net/http/pprof"
)

func prometheusExpose(w io.Writer, name string, v interface{}) {
	// replace all invalid symbols with underscores
	name = strings.Map(func(r rune) rune {
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, name)
	// expvar does not have concepts of counters and gauges,
	// so we tell one from another based on the name suffix.
	counter := strings.HasSuffix(name, "_total")
	if f, ok := v.(float64); ok {
		if counter {
			fmt.Fprintf(w, "# TYPE %s counter\n", name)
		} else {
			fmt.Fprintf(w, "# TYPE %s gauge\n", name)
		}
		fmt.Fprintf(w, "%s %f\n", name, f)
	} else if m, ok := v.(map[string]interface{}); ok {
		for k, v := range m {
			// for composite maps we construct metric names by joining the parent map
			// name and the key name.
			s := strings.TrimSuffix(name, "_total") + "_" + k
			if counter {
				s = s + "_total"
			}
			prometheusExpose(w, s, v)
		}
	}
}

func prometheusHandler(w http.ResponseWriter, r *http.Request) {
	expvar.Do(func(kv expvar.KeyValue) {
		var v interface{}
		if err := json.Unmarshal([]byte(kv.Value.String()), &v); err != nil {
			return
		}
		prometheusExpose(w, kv.Key, v)
	})
}

func prettyLog(v ...interface{}) {
	s := ""
	msg := ""
	if len(v)%2 != 0 {
		msg = fmt.Sprintf("%s", v[0])
		v = v[1:]
	}
	s = fmt.Sprintf("%20s ", msg)
	for i := 0; i < len(v); i = i + 2 {
		s = s + fmt.Sprintf("%v=%v ", v[i], v[i+1])
	}
	log.Println(s)
}

func jsonLog(v ...interface{}) {
	entry := map[string]interface{}{}
	if len(v)%2 != 0 {
		entry["msg"] = v[0]
		v = v[1:]
	}
	for i := 0; i < len(v); i = i + 2 {
		entry[fmt.Sprintf("%v", v[i])] = v[i+1]
	}
	json.NewEncoder(os.Stdout).Encode(entry)
}

type listFlag []string

func (f *listFlag) String() string     { return strings.Join(*f, " ") }
func (f *listFlag) Set(s string) error { *f = append(*f, s); return nil }

func main() {
	gitPaths := listFlag{}
	vcsPaths := listFlag{}

	addr := flag.String("addr", ":0", "http server address")
	verbose := flag.Bool("v", false, "verbose logging")
	prometheus := flag.String("prometheus", "", "prometheus address")
	debug := flag.Bool("debug", false, "enable debug HTTP API (pprof/expvar)")
	json := flag.Bool("json", false, "json structured logging")
	dir := flag.String("dir", filepath.Join(os.Getenv("HOME"), ".gomodproxy/cache"), "modules cache directory")
	gitdir := flag.String("gitdir", filepath.Join(os.Getenv("HOME"), ".gomodproxy/git"), "git cache directory")
	memLimit := flag.Int64("mem", 256, "in-memory cache size in MB")
	workers := flag.Int("workers", 1, "number of parallel VCS workers")
	flag.Var(&gitPaths, "git", "list of git settings")
	flag.Var(&vcsPaths, "vcs", "list of custom VCS handlers")

	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal("net.Listen:", err)
	}
	defer ln.Close()

	fmt.Println("Listening on", ln.Addr())

	options := []api.Option{}
	logger := func(...interface{}) {}
	if *verbose || *json {
		if *json {
			logger = jsonLog
		} else {
			logger = prettyLog
		}
	}
	options = append(options, api.Log(logger))

	for _, path := range gitPaths {
		kv := strings.SplitN(path, ":", 2)
		if len(kv) != 2 {
			log.Fatal("bad git path:", path)
		}
		options = append(options, api.GitWithEphemeralTags(kv[0], kv[1]))
	}

	for _, path := range vcsPaths {
		kv := strings.SplitN(path, ":", 2)
		if len(kv) != 2 {
			log.Fatal("bad VCS syntax:", path)
		}
		options = append(options, api.CustomVCS(kv[0], kv[1]))
	}

	options = append(options,
		api.VCSWorkers(*workers),
		api.GitDir(*gitdir),
		api.Memory(logger, *memLimit*1024*1024),
		api.CacheDir(*dir),
	)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)

	mux := http.NewServeMux()
	mux.Handle("/", api.New(options...))
	if *prometheus != "" {
		if *prometheus == *addr {
			mux.HandleFunc("/metrics", prometheusHandler)
		} else {
			srv := &http.Server{Handler: http.HandlerFunc(prometheusHandler), Addr: *prometheus}
			go srv.ListenAndServe()
		}
	}
	if *debug {
		mux.Handle("/debug/vars", http.DefaultServeMux)
		mux.Handle("/debug/pprof/heap", http.DefaultServeMux)
		mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
		mux.Handle("/debug/pprof/block", http.DefaultServeMux)
		mux.Handle("/debug/pprof/trace", http.DefaultServeMux)
	}

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil {
			log.Fatal(err)
		}
	}()

	<-sigc
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
