package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/config"
	"github.com/Nciae-Zyh/stundeck/internal/engine"
	"github.com/Nciae-Zyh/stundeck/internal/httpapi"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
	"github.com/Nciae-Zyh/stundeck/internal/webhook"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, "stundeck healthcheck:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "stundeck:", err)
		os.Exit(1)
	}
}

func healthcheck() error {
	_, port, err := net.SplitHostPort(config.Load().Listen)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	address := "http://127.0.0.1:" + port + "/api/v1/health"
	if len(os.Args) > 2 {
		address = os.Args[2]
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", response.Status)
	}
	return nil
}

func run() error {
	appConfig := config.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	database, err := store.Open(appConfig.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.DeleteExpiredSessions(context.Background()); err != nil {
		logger.Warn("delete expired sessions", "error", err)
	}
	cipher, err := security.LoadOrCreateCipher(appConfig.MasterKeyFile)
	if err != nil {
		return err
	}
	internalToken, err := security.RandomToken(32)
	if err != nil {
		return err
	}
	callbackURL, err := callbackURL(appConfig.Listen)
	if err != nil {
		return err
	}
	dispatcher := webhook.NewDispatcher(database, cipher, logger)
	manager := engine.NewManager(engine.Config{
		Binary:          appConfig.NatmapBinary,
		NotifyBinary:    appConfig.NotifyBinary,
		CallbackURL:     callbackURL,
		CallbackToken:   internalToken,
		STUNServer:      appConfig.STUNServer,
		KeepAliveServer: appConfig.KeepAliveServer,
		KeepAlive:       appConfig.KeepAlive,
	}, database, logger)
	api := httpapi.New(httpapi.Config{
		Store:         database,
		Cipher:        cipher,
		Engine:        manager,
		Webhooks:      dispatcher,
		Logger:        logger,
		SecureCookies: appConfig.SecureCookies,
		SessionTTL:    appConfig.SessionTTL,
		InternalToken: internalToken,
		StartedAt:     time.Now(),
	})
	httpServer := &http.Server{
		Addr:              appConfig.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go dispatcher.Run(ctx)
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("StunDeck is listening", "address", appConfig.Listen, "engine_available", manager.Available())
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	go restoreServices(ctx, database, manager, logger)
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		return err
	}

	manager.StopAll()
	shutdownContext, cancel := context.WithTimeout(context.Background(), appConfig.ShutdownTimeout)
	defer cancel()
	return httpServer.Shutdown(shutdownContext)
}

func callbackURL(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("invalid listen address: %w", err)
	}
	return "http://127.0.0.1:" + port + "/internal/v1/natmap-event", nil
}

func restoreServices(ctx context.Context, database *store.Store, manager *engine.Manager, logger *slog.Logger) {
	time.Sleep(150 * time.Millisecond)
	services, err := database.EnabledServices(ctx)
	if err != nil {
		logger.Error("restore services", "error", err)
		return
	}
	for _, service := range services {
		if err := manager.Start(ctx, service); err != nil {
			logger.Error("restore service", "service_id", service.ID, "error", err)
			_ = database.SetServiceRuntime(context.Background(), service.ID, "error", err.Error(), true)
		}
	}
}
