package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"localaiserver/internal/config"
	"localaiserver/internal/webui"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// previewHandler exposes only status reads. Inference and control routes are never forwarded.
func previewHandler(backend string, caFile string) (http.Handler, error) {
	target, err := url.Parse(backend)
	if err != nil || !config.IsLoopback(target.Hostname()) || (target.Scheme != "http" && target.Scheme != "https") || target.Path != "" || target.RawQuery != "" || target.User != nil || target.Fragment != "" {
		return nil, errors.New("preview backend must be a loopback HTTP(S) origin")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, errors.New("invalid preview backend CA")
		}
		tc.RootCAs = roots
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = target.Host
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Forwarded")
		pr.Out.Header.Del("X-Forwarded-For")
		pr.Out.Header.Del("X-Forwarded-Host")
		pr.Out.Header.Del("X-Forwarded-Proto")
	}, Transport: &http.Transport{Proxy: nil, TLSClientConfig: tc, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 10 * time.Second}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Del("Set-Cookie")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errors.New("preview backend redirects forbidden")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) { http.Error(w, "gateway unavailable", 503) }}
	ui := webui.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/v1/models" || r.URL.Path == "/v1/status" {
			if r.Method != "GET" || r.URL.RawQuery != "" {
				http.Error(w, "method not allowed", 405)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			r.Body = nil
			proxy.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	}), nil
}
func previewCommand(args []string) error {
	flags := flag.NewFlagSet("web-preview", flag.ContinueOnError)
	listen := flags.String("listen", "0.0.0.0:3000", "development preview listener")
	backend := flags.String("backend", "https://127.0.0.1:8443", "local gateway origin")
	ca := flags.String("ca", "", "gateway CA certificate")
	if err := flags.Parse(args); err != nil {
		return err
	}
	handler, err := previewHandler(*backend, *ca)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
