// Command gopaste is a from-scratch Go reimplementation of haste-server. See
// docs/DESIGN.md for the compatibility contract and roadmap.
package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rake-pro/gopaste/internal/config"
	"github.com/rake-pro/gopaste/internal/handler"
	"github.com/rake-pro/gopaste/internal/keygen"
	"github.com/rake-pro/gopaste/internal/store"
	"github.com/rake-pro/gopaste/web"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to YAML config file (env GOPASTE_CONFIG)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		boot := bootstrapLogger()
		boot.Fatal().Err(err).Msg("load config")
	}
	initLogger(cfg.LogLevel)

	if err := run(cfg); err != nil {
		log.Fatal().Err(err).Msg("gopaste exited with error")
	}
}

func run(cfg config.Config) error {
	ctx := context.Background()

	gen, err := keygen.New(cfg.KeyGenerator.Type, cfg.KeyGenerator.Path)
	if err != nil {
		return err
	}

	log.Info().Str("version", version).Msg("gopaste starting")

	st, err := store.New(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info().Str("backend", cfg.Storage.Type).Msg("storage ready")

	staticKeys := preloadDocuments(ctx, st)

	h, err := handler.New(cfg, st, gen, staticKeys, web.Static())
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	errCh := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Msg("gopaste listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info().Str("signal", sig.String()).Msg("shutting down")
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// preloadDocuments seeds the store with the embedded "about" document (parity
// with upstream config.documents). Returns the set of preloaded keys, whose
// reads must skip TTL bumping. Existing rows (ErrKeyExists) are left as-is.
func preloadDocuments(ctx context.Context, st store.Store) map[string]bool {
	staticKeys := map[string]bool{}
	const aboutKey = "about"
	err := st.Set(ctx, aboutKey, string(web.AboutMD()))
	switch {
	case err == nil, errors.Is(err, store.ErrKeyExists):
		staticKeys[aboutKey] = true
	default:
		log.Warn().Err(err).Msg("preload about document failed; /about will 404")
	}
	return staticKeys
}

// initLogger configures the global zerolog logger. A console (human) writer is
// used when stderr is a TTY; otherwise structured JSON to stderr.
func initLogger(level string) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || level == "" {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)
	zerolog.TimeFieldFormat = time.RFC3339

	if isatty(os.Stderr) {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	} else {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
}

// bootstrapLogger is used before config (and thus log level) is available.
func bootstrapLogger() zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()
}

func isatty(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
