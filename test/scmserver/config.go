package scmserver

import (
	"context"
	"errors"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Config preloads server state at startup. It is read from YAML or JSON.
type Config struct {
	// Behavior overrides the default behavior of providers. Unset fields keep their defaults.
	Behavior map[Provider]map[string]any `json:"behavior,omitempty"`
	// Credentials accepted by providers.
	Credentials []Credential `json:"credentials,omitempty"`
	// GitHubApps registers GitHub Apps.
	GitHubApps []RegisterGitHubAppRequest `json:"githubApps,omitempty"`
	// Repos to create.
	Repos []CreateRepoRequest `json:"repos,omitempty"`
	// Webhooks to subscribe.
	Webhooks []WebhookSubscription `json:"webhooks,omitempty"`
}

// LoadConfig reads a Config from a YAML or JSON file.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from the operator's command line
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.UnmarshalStrict(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// ApplyConfig applies cfg to a started server.
func (s *Server) ApplyConfig(ctx context.Context, cfg Config) error {
	for p, overrides := range cfg.Behavior {
		if !p.Valid() {
			return fmt.Errorf("behavior: unknown provider %q", p)
		}
		merged := s.behavior.get(p)
		b, err := yaml.Marshal(overrides)
		if err != nil {
			return fmt.Errorf("behavior for %s: %w", p, err)
		}
		if err := yaml.UnmarshalStrict(b, &merged); err != nil {
			return fmt.Errorf("behavior for %s: %w", p, err)
		}
		s.behavior.set(p, merged)
	}
	for _, c := range cfg.Credentials {
		if err := s.addCredential(c); err != nil {
			return fmt.Errorf("credential: %w", err)
		}
	}
	for _, app := range cfg.GitHubApps {
		if err := s.registerGitHubApp(app); err != nil {
			return fmt.Errorf("github app %d: %w", app.AppID, err)
		}
	}
	for _, r := range cfg.Repos {
		if _, err := s.CreateRepo(ctx, r); err != nil {
			return fmt.Errorf("repo %s/%s: %w", r.Owner, r.Name, err)
		}
	}
	for _, w := range cfg.Webhooks {
		if !w.Provider.Valid() || w.URL == "" {
			return errors.New("webhook: a valid provider and url are required")
		}
		s.webhooks.add(w)
	}
	return nil
}

// AddCredential registers a token or username/password for a provider.
func (s *Server) AddCredential(c Credential) error {
	return s.addCredential(c)
}

// RegisterGitHubApp registers a GitHub App and its installations.
func (s *Server) RegisterGitHubApp(req RegisterGitHubAppRequest) error {
	return s.registerGitHubApp(req)
}

// AddWebhook subscribes a URL to push events.
func (s *Server) AddWebhook(sub WebhookSubscription) WebhookSubscription {
	return s.webhooks.add(sub)
}

// SetBehavior replaces the behavior of provider p.
func (s *Server) SetBehavior(p Provider, b Behavior) {
	s.behavior.set(p, b)
}

// Behavior returns the current behavior of provider p.
func (s *Server) Behavior(p Provider) Behavior {
	return s.behavior.get(p)
}

// AddFault adds a fault injection rule.
func (s *Server) AddFault(rule FaultRule) (FaultRule, error) {
	return s.faults.add(rule)
}

// ClearFaults removes every fault injection rule.
func (s *Server) ClearFaults() {
	s.faults.reset()
}

// Requests returns the request log.
func (s *Server) Requests() []RequestRecord {
	return s.requests.list()
}

// WebhookDeliveries returns recorded webhook deliveries.
func (s *Server) WebhookDeliveries() []WebhookDelivery {
	return s.webhooks.listDeliveries()
}
