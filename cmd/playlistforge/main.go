// Command playlistforge runs the loopback-only Playlist Forge web application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	"playlistforge/internal/bootstrap"
	"playlistforge/internal/httpapi"
)

const defaultPort = 8787
const defaultHost = "127.0.0.1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "playlist-forge:", err)
		os.Exit(1)
	}
}

func run(args []string) (runErr error) {
	flags := flag.NewFlagSet("playlist-forge", flag.ContinueOnError)
	host := flags.String("host", envString("PLAYLIST_FORGE_HOST", defaultHost), "TCP bind address: 127.0.0.1 or 0.0.0.0")
	port := flags.Int("port", envPort(), "TCP port")
	open := flags.Bool("open-browser", envBool("PLAYLIST_FORGE_OPEN_BROWSER", true), "open the app in the default browser")
	configPath := flags.String("config-dir", envString("PLAYLIST_FORGE_CONFIG_DIR", ""), "application config and database directory")
	logFormat := flags.String("log-format", envString("PLAYLIST_FORGE_LOG_FORMAT", "console"), "console or json")
	logLevel := flags.String("log-level", envString("PLAYLIST_FORGE_LOG_LEVEL", "info"), "debug, info, warn, or error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *port < 1 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if *host != defaultHost && *host != "0.0.0.0" {
		return errors.New("host must be 127.0.0.1 or 0.0.0.0")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := bootstrap.New(bootstrap.Options{Context: ctx, ConfigDir: *configPath, LogFormat: *logFormat, LogLevel: *logLevel})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, application.Close()) }()
	api, err := httpapi.New(application.Service, application.Keys, application.Validator, application.Logger)
	if err != nil {
		return err
	}
	// Keep the host literal here: changing it to an empty host or localhost could
	// expose the unauthenticated, single-user application beyond this machine.
	address := net.JoinHostPort(*host, strconv.Itoa(*port))
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("listen on %s (is the port already in use?): %w", address, err)
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	// A wildcard listener is useful only inside a container. Keep the printed
	// browser URL and Host-header contract on the literal loopback address.
	url := "http://" + net.JoinHostPort(defaultHost, strconv.Itoa(*port)) + "/"
	application.Logger.Info("Playlist Forge is ready", zap.String("url", url), zap.String("config_dir", application.ConfigDir))
	if *open {
		if err := openBrowser(url); err != nil {
			application.Logger.Warn("could not open browser", zap.Error(err))
		}
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func envPort() int {
	value := os.Getenv("PLAYLIST_FORGE_PORT")
	if value == "" {
		return defaultPort
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return defaultPort
	}
	return port
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func resolveConfigDir(override string) (string, error) {
	return bootstrap.ResolveConfigDir(override, "playlist-forge")
}

func openBrowser(url string) error {
	// Starting the platform URL handler is deliberately best-effort; the server
	// remains usable at the URL printed to the console if no GUI is available.
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}
