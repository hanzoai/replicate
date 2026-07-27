package replicate

import (
	"net/http/pprof"

	luxlog "github.com/luxfi/log"
	zip "github.com/zap-proto/zip"
)

// NewApp is the one way replicate builds an HTTP surface: a zip App.
//
// The framework logger is a no-op because replicate logs through slog and zip's
// only chatter on these apps is a construction line — every serve and shutdown
// error is returned to the caller, which logs it on replicate's own stream.
func NewApp(name string) *zip.App {
	return zip.New(zip.Config{AppName: name, Logger: luxlog.NewNoOpLogger()})
}

// RegisterPprof mounts net/http/pprof's endpoints on a zip router.
//
// add is the router method that selects the method set, so the same route table
// serves both surfaces replicate exposes pprof on:
//
//	RegisterPprof(app.Get) // control socket — GET only
//	RegisterPprof(app.All) // metrics addr — net/http.DefaultServeMux never
//	                       // filtered by method, so neither do we
//
// The trailing-slash pair mirrors net/http.ServeMux subtree semantics:
// "/debug/pprof/" matches the index itself and "/debug/pprof/*" matches the
// profile names underneath it (heap, goroutine, allocs, ...), which pprof.Index
// resolves from the request path. The four named profiles are registered
// explicitly and win over the wildcard by route specificity.
func RegisterPprof(add func(path string, handlers ...zip.Handler) zip.Router) {
	add("/debug/pprof/cmdline", zip.AdaptNetHTTPFunc(pprof.Cmdline))
	add("/debug/pprof/profile", zip.AdaptNetHTTPFunc(pprof.Profile))
	add("/debug/pprof/symbol", zip.AdaptNetHTTPFunc(pprof.Symbol))
	add("/debug/pprof/trace", zip.AdaptNetHTTPFunc(pprof.Trace))
	add("/debug/pprof/", zip.AdaptNetHTTPFunc(pprof.Index))
	add("/debug/pprof/*", zip.AdaptNetHTTPFunc(pprof.Index))
}
