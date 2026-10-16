// Command scmserver runs the test SCM server, which impersonates GitHub, GitLab, Azure DevOps, Bitbucket Cloud,
// Gitea and Forgejo for e2e tests and local development. It is not hardened and must never be exposed outside a
// test environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

type options struct {
	listen     map[scmserver.Provider]*string
	adminAddr  string
	dataDir    string
	publicHost string
	hostnames  []string
	tlsCert    string
	tlsKey     string
	caCert     string
	caOut      string
	config     string
	logLevel   string
	tokens     []string
}

func main() {
	if err := newCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

var defaultPorts = map[scmserver.Provider]string{
	scmserver.GitHub:         ":9001",
	scmserver.GitLab:         ":9002",
	scmserver.AzureDevOps:    ":9003",
	scmserver.BitbucketCloud: ":9004",
	scmserver.Gitea:          ":9005",
	scmserver.Forgejo:        ":9006",
}

func newCommand() *cobra.Command {
	opts := options{listen: map[scmserver.Provider]*string{}}
	cmd := &cobra.Command{
		Use:   "scmserver",
		Short: "Test SCM server impersonating every SCM provider supported by GitOps Promoter",
		Long: `scmserver serves the REST APIs of GitHub Enterprise, GitLab, Azure DevOps, Bitbucket Cloud, Gitea and
Forgejo over HTTPS, one listener per provider, backed by real git repositories. Point an ScmProvider's "domain"
field at a listener and trust the CA to run the real provider implementations against it.

Not hardened: for tests and local development only.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	for _, p := range scmserver.AllProviders {
		opts.listen[p] = f.String(string(p)+"-addr", defaultPorts[p], "listen address of the "+string(p)+" HTTPS API")
	}
	f.StringVar(&opts.adminAddr, "admin-addr", ":9000", "listen address of the plain HTTP admin API")
	f.StringVar(&opts.dataDir, "data-dir", "", "directory for bare git repositories (default: a temporary directory)")
	f.StringVar(&opts.publicHost, "public-host", "localhost", "host name used in URLs the server generates, such as webhook payloads")
	f.StringSliceVar(&opts.hostnames, "hostnames", nil, "extra DNS names or IPs for the self-generated serving certificate")
	f.StringVar(&opts.tlsCert, "tls-cert", "", "PEM serving certificate (default: generate a CA and certificate)")
	f.StringVar(&opts.tlsKey, "tls-key", "", "PEM serving key for --tls-cert")
	f.StringVar(&opts.caCert, "ca-cert", "", "PEM CA certificate served at /ca.pem when --tls-cert is set")
	f.StringVar(&opts.caOut, "ca-out", "", "write the CA certificate clients must trust to this file")
	f.StringVar(&opts.config, "config", "", "YAML or JSON file with repositories, credentials, GitHub Apps, webhooks and behavior to preload")
	f.StringSliceVar(&opts.tokens, "token", nil, "access token accepted by every provider (repeatable)")
	f.StringVar(&opts.logLevel, "log-level", "info", "log level: debug, info, warn or error")
	return cmd
}

func run(ctx context.Context, opts options) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.logLevel)); err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverOpts := scmserver.Options{
		Logger:      logger,
		Listen:      map[scmserver.Provider]string{},
		AdminListen: opts.adminAddr,
		DataDir:     opts.dataDir,
		PublicHost:  opts.publicHost,
	}
	for p, addr := range opts.listen {
		serverOpts.Listen[p] = *addr
	}
	if err := loadTLS(&serverOpts, opts); err != nil {
		return err
	}

	srv, err := scmserver.New(ctx, serverOpts)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "error", err)
		}
	}()

	if opts.caOut != "" && len(srv.CAPEM()) > 0 {
		if err := os.WriteFile(opts.caOut, srv.CAPEM(), 0o600); err != nil {
			return fmt.Errorf("write CA: %w", err)
		}
		logger.Info("wrote CA certificate", "path", opts.caOut)
	}
	for _, token := range opts.tokens {
		for _, p := range scmserver.AllProviders {
			if err := srv.AddCredential(scmserver.Credential{Provider: p, Token: token}); err != nil {
				return fmt.Errorf("add token: %w", err)
			}
		}
	}
	if opts.config != "" {
		cfg, err := scmserver.LoadConfig(opts.config)
		if err != nil {
			return err //nolint:wrapcheck // LoadConfig errors name the file
		}
		if err := srv.ApplyConfig(ctx, cfg); err != nil {
			return fmt.Errorf("apply config: %w", err)
		}
	}

	<-ctx.Done()
	return nil
}

// loadTLS reads the serving certificate, or generates one when none is given.
func loadTLS(serverOpts *scmserver.Options, opts options) error {
	if opts.tlsCert == "" {
		if opts.tlsKey != "" {
			return errors.New("--tls-key requires --tls-cert")
		}
		hosts := append([]string{opts.publicHost, "localhost", "127.0.0.1", "::1"}, opts.hostnames...)
		caPEM, caKey, err := scmserver.GenerateCA("scmserver test CA")
		if err != nil {
			return fmt.Errorf("generate CA: %w", err)
		}
		certPEM, keyPEM, err := scmserver.GenerateServingCert(caPEM, caKey, dedupe(hosts))
		if err != nil {
			return fmt.Errorf("generate serving certificate: %w", err)
		}
		serverOpts.CertPEM, serverOpts.KeyPEM, serverOpts.CAPEM = certPEM, keyPEM, caPEM
		return nil
	}
	certPEM, err := os.ReadFile(opts.tlsCert)
	if err != nil {
		return fmt.Errorf("read --tls-cert: %w", err)
	}
	keyPEM, err := os.ReadFile(opts.tlsKey)
	if err != nil {
		return fmt.Errorf("read --tls-key: %w", err)
	}
	serverOpts.CertPEM, serverOpts.KeyPEM = certPEM, keyPEM
	if opts.caCert != "" {
		if serverOpts.CAPEM, err = os.ReadFile(opts.caCert); err != nil {
			return fmt.Errorf("read --ca-cert: %w", err)
		}
	}
	return nil
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
