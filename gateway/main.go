package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	socketPath string
	upstream   *url.URL
	prefix     string
}

func parseConfig() (config, error) {
	var socketPath, upstreamRaw, prefix string
	flag.StringVar(&socketPath, "socket", "", "Unix socket path exposed to the fnOS gateway")
	flag.StringVar(&upstreamRaw, "upstream", "http://127.0.0.1:8096", "Emby upstream URL")
	flag.StringVar(&prefix, "prefix", "/app/emby", "fnOS gateway path prefix")
	flag.Parse()

	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return config{}, errors.New("socket must be an absolute path")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" || upstream.Path != "" {
		return config{}, fmt.Errorf("invalid HTTP upstream %q", upstreamRaw)
	}
	if !strings.HasPrefix(prefix, "/") || prefix == "/" || strings.HasSuffix(prefix, "/") {
		return config{}, fmt.Errorf("prefix must look like /app/name without a trailing slash")
	}
	return config{socketPath: socketPath, upstream: upstream, prefix: prefix}, nil
}

func stripGatewayPrefix(path, prefix string) string {
	if path == prefix {
		return "/"
	}
	if strings.HasPrefix(path, prefix+"/") {
		return strings.TrimPrefix(path, prefix)
	}
	return path
}

func addGatewayPrefix(path, prefix string) string {
	if path == prefix || strings.HasPrefix(path, prefix+"/") {
		return path
	}
	if path == "" || path == "/" {
		return prefix + "/"
	}
	return prefix + "/" + strings.TrimPrefix(path, "/")
}

func newReverseProxy(upstream *url.URL, prefix string) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	defaultDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalHost := request.Host
		request.URL.Path = stripGatewayPrefix(request.URL.Path, prefix)
		if request.URL.RawPath != "" {
			request.URL.RawPath = stripGatewayPrefix(request.URL.RawPath, prefix)
		}
		defaultDirector(request)
		request.Host = originalHost
		request.Header.Set("X-Forwarded-Host", originalHost)
		request.Header.Set("X-Forwarded-Prefix", prefix)
		if request.Header.Get("X-Forwarded-Proto") == "" {
			request.Header.Set("X-Forwarded-Proto", "https")
		}
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		location := response.Header.Get("Location")
		if parsed, err := url.Parse(location); err == nil && parsed.IsAbs() && parsed.Host == upstream.Host {
			parsed.Scheme = response.Request.Header.Get("X-Forwarded-Proto")
			parsed.Host = response.Request.Host
			parsed.Path = addGatewayPrefix(parsed.Path, prefix)
			response.Header.Set("Location", parsed.String())
		} else if strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") {
			response.Header.Set("Location", addGatewayPrefix(location, prefix))
		}
		cookies := response.Cookies()
		if len(cookies) > 0 {
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Path == "" || cookie.Path == "/" {
					cookie.Path = prefix
				}
				response.Header.Add("Set-Cookie", cookie.String())
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		log.Printf("proxy error for %s %s: %v", request.Method, request.URL.Path, err)
		http.Error(writer, "Emby upstream unavailable", http.StatusBadGateway)
	}
	return proxy
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	return os.Remove(path)
}

func run(ctx context.Context, cfg config) error {
	if err := removeStaleSocket(cfg.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", cfg.socketPath)
	if err != nil {
		return fmt.Errorf("listen on unix socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(cfg.socketPath)
	if err := os.Chmod(cfg.socketPath, 0660); err != nil {
		return fmt.Errorf("chmod unix socket: %w", err)
	}

	server := &http.Server{
		Handler:           newReverseProxy(cfg.upstream, cfg.prefix),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("fnOS gateway listening on unix://%s and forwarding %s to %s", cfg.socketPath, cfg.prefix, cfg.upstream)

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-done
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}
