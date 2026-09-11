package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/merzzzl/openapi-mcp-server/internal/clients"
	mcpctrl "github.com/merzzzl/openapi-mcp-server/internal/controller/mcp"
	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
	"github.com/merzzzl/openapi-mcp-server/internal/models"
	oapirepo "github.com/merzzzl/openapi-mcp-server/internal/repository/openapi"
	proxyrepo "github.com/merzzzl/openapi-mcp-server/internal/repository/proxy"
	"github.com/merzzzl/openapi-mcp-server/internal/service/bugindex"
	"github.com/merzzzl/openapi-mcp-server/internal/service/schema"
	"github.com/merzzzl/openapi-mcp-server/internal/service/tool"
	zentaosvc "github.com/merzzzl/openapi-mcp-server/internal/service/zentao"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	shutdownTimeout = 10 * time.Second
)

func main() {
	exitCode := 0

	defer func() {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	cfgPath := flag.String("config", "config.yaml", "config file path")

	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		slog.ErrorContext(ctx, "load config", "error", err)

		exitCode = 1

		return
	}

	shutdownTelemetry, err := initTelemetry(ctx, &cfg.Telemetry)
	if err != nil {
		slog.ErrorContext(ctx, "init telemetry", "error", err)

		exitCode = 1

		return
	}

	defer func() {
		tCtx, tCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer tCancel()

		if err := shutdownTelemetry(tCtx); err != nil {
			slog.ErrorContext(ctx, "shutdown telemetry", "error", err)
		}
	}()

	if err := zentaosvc.SetLocation(cfg.Timezone); err != nil {
		slog.ErrorContext(ctx, "invalid timezone", "error", err)

		exitCode = 1

		return
	}

	slog.InfoContext(ctx, "zentao timezone", "location", zentaosvc.LocationName())

	health := newHealthServer()

	go health.Start(ctx)

	slog.InfoContext(ctx, "configuration loaded", "servers", len(cfg.Servers), "listen", cfg.Port)

	mux := http.NewServeMux()

	for i := range cfg.Servers {
		sc := &cfg.Servers[i]

		httpClient := clients.NewHTTPClient(http.DefaultClient, sc.BaseURL)

		loader := oapirepo.New(httpClient, sc.SchemaURL)
		proxy := proxyrepo.New(httpClient)

		schemaSvc := schema.New(loader)
		toolSvc := tool.New(proxy, sc.BaseURL)

		allow, err := compileMatchRules(sc.Allow)
		if err != nil {
			slog.ErrorContext(ctx, "compile allow rules", "error", err)

			exitCode = 1

			return
		}

		block, err := compileMatchRules(sc.Block)
		if err != nil {
			slog.ErrorContext(ctx, "compile block rules", "error", err)

			exitCode = 1

			return
		}

		matcher := models.NewOperationMatcher(allow, block)

		opts := mcpctrl.Options{
			EnableTOON:          cfg.EnableTOON,
			SkipDeprecated:      sc.SkipDeprecated,
			PublishOutputSchema: sc.publishOutputSchema(),
		}

		if sc.ZentaoExtensions {
			zsvc := zentaosvc.New(proxy, sc.BaseURL)

			if err := attachBugIndex(ctx, zsvc, sc); err != nil {
				slog.ErrorContext(ctx, "configure bug index", "server", sc.Name, "error", err)

				exitCode = 1

				return
			}

			opts.Zentao = zsvc
		}

		ctrl := mcpctrl.New(schemaSvc, toolSvc, matcher.IsAllowed, opts)

		server := mcp.NewServer(&mcp.Implementation{Name: sc.Name}, nil)

		if err := ctrl.RegisterAllTools(ctx, server); err != nil {
			slog.ErrorContext(ctx, "register tools", "server", sc.Name, "error", err)

			exitCode = 1

			return
		}

		pattern := "/" + sc.Name

		mcpH := newStreamableMCPHandler(server)
		sseH := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)

		mux.Handle(pattern+"/mcp", middleware.NewLoggingHandler(middleware.NewAuthorizationHandler(mcpH)))
		mux.Handle(pattern+"/sse", middleware.NewLoggingHandler(middleware.NewAuthorizationHandler(sseH)))

		slog.InfoContext(ctx, "server registered", "name", sc.Name, "mcp", pattern+"/mcp", "sse", pattern+"/sse")
	}

	health.SetReady(true)

	srv := &http.Server{
		Addr:    cfg.Port,
		Handler: mux,
	}

	go func() {
		slog.InfoContext(ctx, "starting server", "port", cfg.Port)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "server error", "error", err)
		}
	}()

	<-ctx.Done()

	slog.InfoContext(ctx, "shutting down server")

	sCtx, sCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer sCancel()

	if err := srv.Shutdown(sCtx); err != nil {
		slog.ErrorContext(ctx, "failed to shutdown server", "error", err)
	}
}

// attachBugIndex wires the historical bug corpus onto a ZenTao service and
// starts its background builder. A missing password disables the feature with
// a warning rather than failing startup, so the rest of the server still runs.
func attachBugIndex(ctx context.Context, svc *zentaosvc.Service, sc *ServerConfig) error {
	if sc.BugIndex == nil || !sc.BugIndex.Enabled {
		return nil
	}

	refresh, err := sc.BugIndex.refreshDuration()
	if err != nil {
		return err
	}

	password := sc.BugIndex.resolvePassword()

	if sc.BugIndex.Account == "" || password == "" {
		slog.WarnContext(ctx, "bug index enabled but has no service account; skipping",
			"server", sc.Name,
			"password_env", sc.BugIndex.PasswordEnv,
		)

		return nil
	}

	svc.AttachIndex(bugindex.New(), zentaosvc.IndexOptions{
		Account:           sc.BugIndex.Account,
		Password:          password,
		Refresh:           refresh,
		BodyChars:         sc.BugIndex.BodyChars,
		MaxProducts:       sc.BugIndex.MaxProducts,
		MaxBugsPerProduct: sc.BugIndex.MaxBugsPerProduct,
	})

	go svc.RunIndexBuilder(ctx)

	slog.InfoContext(ctx, "bug index builder started", "server", sc.Name, "account", sc.BugIndex.Account)

	return nil
}

func newStreamableMCPHandler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	)
}
