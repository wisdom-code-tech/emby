package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestReverseProxyStripsGatewayPrefix(t *testing.T) {
	received := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.Clone(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/emby"))
	defer proxy.Close()

	request, err := http.NewRequest(http.MethodGet, proxy.URL+"/app/emby/emby/System/Info?api_key=test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "nas.example.test"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()

	forwarded := <-received
	if forwarded.URL.Path != "/emby/System/Info" {
		t.Fatalf("gateway prefix was not stripped: %s", forwarded.URL.Path)
	}
	if forwarded.URL.RawQuery != "api_key=test" {
		t.Fatalf("query was rewritten: %s", forwarded.URL.RawQuery)
	}
	if forwarded.Host != "nas.example.test" {
		t.Fatalf("host was not preserved: %s", forwarded.Host)
	}
	if forwarded.Header.Get("X-Forwarded-Prefix") != "/app/emby" {
		t.Fatalf("missing forwarded prefix")
	}
}

func TestReverseProxyKeepsRootRequest(t *testing.T) {
	received := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.URL.Path
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/emby"))
	defer proxy.Close()
	response, err := http.Get(proxy.URL + "/embywebsocket")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if path := <-received; path != "/embywebsocket" {
		t.Fatalf("root request was changed: %s", path)
	}
}

func TestReverseProxyPrefixesRedirectsAndCookies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: "session", Value: "value", Path: "/", HttpOnly: true})
		http.Redirect(writer, request, "/web/index.html", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/emby"))
	defer proxy.Close()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(proxy.URL + "/app/emby/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if location := response.Header.Get("Location"); location != "/app/emby/web/index.html" {
		t.Fatalf("unexpected redirect: %s", location)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/app/emby" {
		t.Fatalf("unexpected cookies: %#v", cookies)
	}
}

func TestReverseProxyRewritesAbsoluteUpstreamRedirect(t *testing.T) {
	var upstreamURL *url.URL
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", upstreamURL.String()+"/web/index.html")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	upstreamURL, _ = url.Parse(upstream.URL)
	proxy := httptest.NewServer(newReverseProxy(upstreamURL, "/app/emby"))
	defer proxy.Close()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequest(http.MethodGet, proxy.URL+"/app/emby/", nil)
	request.Host = "nas.example.test"
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if location := response.Header.Get("Location"); location != "https://nas.example.test/app/emby/web/index.html" {
		t.Fatalf("unexpected absolute redirect: %s", location)
	}
}

func TestRemoveStaleSocketRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emby.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(path); err == nil {
		t.Fatal("regular file was accepted")
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_EMBY_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp", os.Getenv("EMBY_HELPER_ADDR"))
	if err != nil {
		os.Exit(2)
	}
	defer listener.Close()
	child := exec.Command("sleep", "300")
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("EMBY_HELPER_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		os.Exit(4)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
	// 故意不 wait 子进程，用于验证网关的 subreaper 能回收被收养的后代。
	os.Exit(0)
}

func TestSupervisorStopsAndReapsProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process group and subreaper test")
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	probe.Close()

	tempDir := t.TempDir()
	wrapper := filepath.Join(tempDir, "emby-helper")
	wrapperBody := "#!/bin/sh\nexec \"$EMBY_HELPER_BINARY\" -test.run=TestProcessHelper\n"
	if err := os.WriteFile(wrapper, []byte(wrapperBody), 0700); err != nil {
		t.Fatal(err)
	}
	childPIDFile := filepath.Join(tempDir, "child.pid")
	t.Setenv("GO_WANT_EMBY_HELPER", "1")
	t.Setenv("EMBY_HELPER_BINARY", os.Args[0])
	t.Setenv("EMBY_HELPER_ADDR", address)
	t.Setenv("EMBY_HELPER_CHILD_PID", childPIDFile)
	upstream, _ := url.Parse("http://" + address)
	supervisor := &supervisor{cfg: config{
		embyPath: wrapper,
		embyRoot: tempDir,
		dataPath: tempDir,
		upstream: upstream,
	}}
	if err := supervisor.start(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(childPID))); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child process %d was not reaped", childPID)
}
