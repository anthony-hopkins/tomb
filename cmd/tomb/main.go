// Command tomb serves the TOMB guild platform: Battle.net SSO plus the
// registrable apps built on the platform core.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/comingsoon"
	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/apps/guild"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

func main() {
	// compose.yaml's healthcheck runs the binary itself rather than adding curl
	// to a distroless image.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /readyz endpoint and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeReadiness())
	}

	if err := run(); err != nil {
		// The logger may not exist yet, so fail on stderr.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := platform.NewLogger()

	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Closing on the way out of the process; there is nothing to do with an
	// error from it.
	defer func() { _ = db.Close() }()

	if err := platform.Migrate(ctx, db); err != nil {
		return err
	}
	logger.Info("database ready")

	templates, err := platform.LoadTemplates()
	if err != nil {
		return err
	}

	bnet := blizzard.NewHTTPClient(cfg.APIHost, cfg.Namespace(), cfg.BnetRegion)
	// Zero -- the default, with TOMB_GUILD_ROSTER_TTL unset -- means the roster
	// is fetched live on every view. A previously fetched roster is still kept,
	// but only as a fallback for when Blizzard cannot be reached.
	bnet.RosterTTL = cfg.GuildRosterTTL

	store := &auth.Store{DB: db}
	sessions := &auth.SessionManager{Store: store, CookieSecure: cfg.SessionCookieSecure}
	csrf := &platform.CSRF{Secure: cfg.SessionCookieSecure}

	// One guild identity, built once and shared: the membership check uses it
	// and so does every app that is about the guild.
	guildCfg := platform.GuildConfig{
		Name:      cfg.GuildName,
		RealmSlug: cfg.GuildRealm,
		Ranks:     cfg.GuildRanks,
	}

	core := &platform.Core{
		Deps: platform.Deps{
			DB:       db,
			Blizzard: bnet,
			Logger:   logger,
			Config:   cfg,
			Guild:    guildCfg,
		},
		Sessions: sessions,
		Profiles: &platform.ProfileFetcher{
			Client: bnet,
			Guild:  guildCfg,
			Logger: logger,
		},
		CSRF:      csrf,
		Templates: templates,
	}
	// Apps render their own bodies; the core owns the shell.
	core.Deps.RenderInLayout = core.RenderInLayout

	authHandlers := &auth.Handlers{
		OAuth:    auth.NewOAuthConfig(cfg.BnetClientID, cfg.BnetClientSecret, cfg.BnetRedirectURL),
		Sessions: sessions,
		Store:    store,
		Blizzard: bnet,
		Logger:   logger,
		Gate:     core,
		Renderer: core,
		CSRF:     csrf,
	}

	characterDashboard, err := dashboard.New(core.Deps)
	if err != nil {
		return fmt.Errorf("build dashboard app: %w", err)
	}

	guildOverview, err := guild.New(core.Deps)
	if err != nil {
		return fmt.Errorf("build guild app: %w", err)
	}

	comingSoon, err := comingsoon.New(core.Deps)
	if err != nil {
		return fmt.Errorf("build coming soon app: %w", err)
	}

	// The single registration point. Adding an app means adding one line here
	// and nothing else (Principle II, contracts/app-registration.md).
	//
	// Order here is not nav order -- Mount sorts navigation by label -- and it
	// is not home either: home is the app that declares AppMeta.Home, which is
	// the guild overview, reached through the TOMB brand link rather than a nav
	// entry of its own.
	apps := []platform.App{
		guildOverview,
		characterDashboard,
		comingSoon,
	}

	handler, err := platform.Mount(core, authHandlers, apps)
	if err != nil {
		// Duplicate slugs and prefix mismatches fail startup, not a request.
		return err
	}

	go sweepSessions(ctx, store, logger)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// A dashboard view fans out to Blizzard, so the write timeout must
		// exceed the fan-out deadline.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr, "region", cfg.BnetRegion, "guild", cfg.GuildName)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// sweepSessions periodically deletes expired sessions. Expiry is always
// enforced at read time too, so this is housekeeping rather than a guarantee
// (data-model.md lifecycle).
func sweepSessions(ctx context.Context, store *auth.Store, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.SweepExpired(ctx)
			if err != nil {
				logger.Error("sweep expired sessions", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("swept expired sessions", "count", n)
			}
		}
	}
}

// probeReadiness is the container healthcheck: hit /readyz on localhost.
func probeReadiness() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "8080"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/readyz")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
