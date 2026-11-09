package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"io"
	"localaiserver/internal/config"
	"localaiserver/internal/gateway"
	"localaiserver/internal/webui"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// previewHandler exposes only status reads. Inference and control routes are never forwarded.
func previewHandler(backend string, caFile string) (http.Handler, error) {
	return browserHandler(backend, caFile, false)
}

func browserHandler(backend string, caFile string, management bool, profiles ...config.Config) (http.Handler, error) {
	budget := config.ProxyTimeout
	bodyLimit := int64(config.MaxRequestBytes)
	concurrent := 64
	if len(profiles) > 0 {
		budget = profiles[0].ProxyBudget()
		bodyLimit = profiles[0].MaxBodyBytes
		concurrent = profiles[0].ConcurrentLimit()
	}
	ingress := make(chan struct{}, concurrent)
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
	proxy := &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = target.Host
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Forwarded")
		pr.Out.Header.Del("X-Forwarded-For")
		pr.Out.Header.Del("X-Forwarded-Host")
		pr.Out.Header.Del("X-Forwarded-Proto")
	}, Transport: &http.Transport{Proxy: nil, TLSClientConfig: tc, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: budget}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Del("Set-Cookie")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return errors.New("preview backend redirects forbidden")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		gateway.WriteError(w, 503, "model_unavailable", "Gateway unavailable")
	}}
	ui := webui.Handler()
	return gateway.StrictPaths(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case ingress <- struct{}{}:
			defer func() { <-ingress }()
		default:
			gateway.WriteError(w, 429, "model_busy", "Request concurrency limit reached")
			return
		}
		if management && (strings.HasPrefix(r.URL.Path, "/admin/") || r.URL.Path == "/v1/chat/completions") {
			if r.Method != "GET" && r.Method != "POST" && r.Method != "PUT" {
				http.Error(w, "method not allowed", 405)
				return
			}
			if r.URL.RawQuery != "" {
				http.Error(w, "query not allowed", 400)
				return
			}
			// Bound the upload separately; generation gets its full budget only after
			// the bounded body has arrived. Never persist the buffered payload.
			limit := int64(1 << 20)
			if r.URL.Path == "/v1/chat/completions" {
				limit = bodyLimit
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			r.Body.Close()
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "request too large", 413)
				} else {
					http.Error(w, "invalid request body", 400)
				}
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
			r = r.WithContext(ctx)
			proxy.ServeHTTP(w, r)
			return
		}
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
	})), nil
}
func previewCommand(args []string) error {
	flags := flag.NewFlagSet("web-preview", flag.ContinueOnError)
	listen := flags.String("listen", "0.0.0.0:3000", "development preview listener")
	backend := flags.String("backend", "https://127.0.0.1:8443", "local gateway origin")
	management := flags.Bool("management", false, "enable dashboard management and API test forwarding (loopback only)")
	ca := flags.String("ca", "", "gateway CA certificate")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *management {
		host, _, err := net.SplitHostPort(*listen)
		if err != nil || !config.IsLoopback(host) {
			return errors.New("management browser listener must be loopback")
		}
	}
	handler, err := browserHandler(*backend, *ca, *management)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: config.UploadTimeout, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := terminationContext()
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
