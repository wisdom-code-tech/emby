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
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	socketPath string
	upstream   *url.URL
	prefix     string
	embyPath   string
	embyRoot   string
	dataPath   string
}

func parseConfig() (config, error) {
	var socketPath, upstreamRaw, prefix, embyPath, embyRoot, dataPath string
	flag.StringVar(&socketPath, "socket", "", "Unix socket path exposed to the fnOS gateway")
	flag.StringVar(&upstreamRaw, "upstream", "http://127.0.0.1:8096", "Emby upstream URL")
	flag.StringVar(&prefix, "prefix", "/app/emby", "fnOS gateway path prefix")
	flag.StringVar(&embyPath, "emby", "", "EmbyServer executable managed by this gateway")
	flag.StringVar(&embyRoot, "emby-root", "", "Emby Server installation root")
	flag.StringVar(&dataPath, "data", "", "Emby program data directory")
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
	for name, path := range map[string]string{"emby": embyPath, "emby-root": embyRoot, "data": dataPath} {
		if path == "" || !filepath.IsAbs(path) {
			return config{}, fmt.Errorf("%s must be an absolute path", name)
		}
	}
	return config{socketPath: socketPath, upstream: upstream, prefix: prefix, embyPath: embyPath, embyRoot: embyRoot, dataPath: dataPath}, nil
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

type supervisor struct {
	mu      sync.Mutex
	cfg     config
	command *exec.Cmd
	done    chan error
}

func (s *supervisor) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.command != nil {
		return nil
	}
	if connection, err := net.DialTimeout("tcp", s.cfg.upstream.Host, 500*time.Millisecond); err == nil {
		connection.Close()
		return fmt.Errorf("refusing to start Emby because %s is already in use", s.cfg.upstream.Host)
	}
	args := []string{
		"-programdata", s.cfg.dataPath,
		"-ffdetect", filepath.Join(s.cfg.embyRoot, "bin", "ffdetect"),
		"-ffmpeg", filepath.Join(s.cfg.embyRoot, "bin", "ffmpeg"),
		"-ffprobe", filepath.Join(s.cfg.embyRoot, "bin", "ffprobe"),
		"-restartexitcode", "3",
	}
	command := exec.Command(s.cfg.embyPath, args...)
	command.Dir = s.cfg.embyRoot
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = embySysProcAttr()
	if err := prepareChildSupervisor(); err != nil {
		return fmt.Errorf("prepare child supervisor: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Emby: %w", err)
	}
	s.command = command
	s.done = make(chan error, 1)
	go func(done chan<- error) { done <- command.Wait() }(s.done)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			s.command = nil
			s.done = nil
			if err == nil {
				return fmt.Errorf("Emby exited before listening on %s", s.cfg.upstream.Host)
			}
			return fmt.Errorf("Emby exited before listening on %s: %w", s.cfg.upstream.Host, err)
		default:
		}
		connection, err := net.DialTimeout("tcp", s.cfg.upstream.Host, 500*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.stopLocked()
	return fmt.Errorf("Emby did not listen on %s within 60s", s.cfg.upstream.Host)
}

func (s *supervisor) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *supervisor) markExited(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.command != nil && s.command.Process != nil {
		pid := s.command.Process.Pid
		_ = signalProcessGroup(pid, syscall.SIGKILL)
		reapProcessGroup(pid, 3*time.Second)
	}
	s.command = nil
	s.done = nil
	if err == nil {
		return errors.New("Emby exited unexpectedly")
	}
	return fmt.Errorf("Emby exited unexpectedly: %w", err)
}

func (s *supervisor) stopLocked() error {
	if s.command == nil || s.command.Process == nil {
		s.command = nil
		s.done = nil
		return nil
	}
	pid := s.command.Process.Pid
	_ = signalProcessGroup(pid, syscall.SIGTERM)
	select {
	case err := <-s.done:
		if err != nil {
			log.Printf("Emby exited: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = signalProcessGroup(pid, syscall.SIGKILL)
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			log.Printf("timed out reaping Emby process group %d", pid)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for processGroupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if processGroupAlive(pid) {
		_ = signalProcessGroup(pid, syscall.SIGKILL)
	}
	reapProcessGroup(pid, 3*time.Second)
	s.command = nil
	s.done = nil
	return nil
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
	supervisor := &supervisor{cfg: cfg}
	if err := supervisor.start(); err != nil {
		return err
	}
	defer supervisor.stop()
	embyDone := supervisor.done

	server := &http.Server{
		Handler:           newReverseProxy(cfg.upstream, cfg.prefix),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("fnOS gateway listening on unix://%s and forwarding %s to %s", cfg.socketPath, cfg.prefix, cfg.upstream)

	select {
	case err := <-embyDone:
		_ = server.Close()
		<-done
		return supervisor.markExited(err)
	case err := <-done:
		stopErr := supervisor.stop()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, stopErr)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		stopErr := supervisor.stop()
		serveErr := <-done
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, stopErr, serveErr)
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
