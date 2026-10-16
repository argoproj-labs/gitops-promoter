package scmserver

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxGitHubJWTLifetime is the longest JWT lifetime GitHub accepts.
const maxGitHubJWTLifetime = 10 * time.Minute

var errUnauthorized = errors.New("bad credentials")

// principal is an authenticated API or git caller.
type principal struct {
	// name identifies the caller in commits, events and the request log.
	name string
	// key identifies the credential for rate limiting.
	key string
	// installationID is set for GitHub App installation tokens.
	installationID int64
}

// userID derives a stable numeric user ID from the credential, for messages that name one.
func (p principal) userID() uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(p.key))
	return h.Sum32()%1_000_000 + 1
}

// Credential registers an access token or username/password a provider accepts.
type Credential struct {
	// Provider accepting the credential.
	Provider Provider `json:"provider"`
	// Token is a personal access token (GitLab, Azure DevOps, Bitbucket Cloud, Gitea, Forgejo, or a GitHub PAT).
	Token string `json:"token,omitempty"`
	// Username and Password register basic-auth credentials (Gitea and Forgejo).
	Username string `json:"username,omitempty"`
	// Password is the basic-auth password for Username.
	Password string `json:"password,omitempty"`
}

func (s *Server) addCredential(c Credential) error {
	if !c.Provider.Valid() {
		return fmt.Errorf("unknown provider %q", c.Provider)
	}
	if c.Token == "" && c.Username == "" {
		return errors.New("a token or a username is required")
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if c.Token != "" {
		if s.store.tokens[c.Provider] == nil {
			s.store.tokens[c.Provider] = map[string]bool{}
		}
		s.store.tokens[c.Provider][c.Token] = true
	}
	if c.Username != "" {
		if s.store.basicAuth[c.Provider] == nil {
			s.store.basicAuth[c.Provider] = map[string]string{}
		}
		s.store.basicAuth[c.Provider][c.Username] = c.Password
	}
	return nil
}

// tokenPrincipal validates a provider token (PAT or GitHub installation token).
func (s *Server) tokenPrincipal(p Provider, token string) (principal, error) {
	if token == "" {
		return principal{}, errUnauthorized
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if p == GitHub {
		if it, ok := s.store.installTokens[token]; ok {
			if !s.now().Before(it.expiresAt) {
				return principal{}, errUnauthorized
			}
			return principal{
				name:           "promoter-app[bot]",
				key:            "installation:" + strconv.FormatInt(it.installationID, 10),
				installationID: it.installationID,
			}, nil
		}
	}
	if s.store.tokens[p][token] {
		return principal{name: tokenUser(p), key: "token:" + token}, nil
	}
	return principal{}, errUnauthorized
}

// tokenUser is the user name a provider associates with an access token in this server.
func tokenUser(p Provider) string {
	return string(p) + "-user"
}

func (s *Server) basicPrincipal(p Provider, user, password string) (principal, error) {
	if pr, err := s.tokenPrincipal(p, password); err == nil {
		return pr, nil
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if want, ok := s.store.basicAuth[p][user]; ok && want == password {
		return principal{name: user, key: "user:" + user}, nil
	}
	return principal{}, errUnauthorized
}

// authenticateAPI validates the credentials of a REST API request the way provider p does.
func (s *Server) authenticateAPI(p Provider, r *http.Request) (principal, error) {
	authz := r.Header.Get("Authorization")
	scheme, value, _ := strings.Cut(authz, " ")
	switch p {
	case GitHub:
		if strings.EqualFold(scheme, "token") || strings.EqualFold(scheme, "bearer") {
			return s.tokenPrincipal(p, value)
		}
	case GitLab:
		if token := r.Header.Get("Private-Token"); token != "" {
			return s.tokenPrincipal(p, token)
		}
		if strings.EqualFold(scheme, "bearer") {
			return s.tokenPrincipal(p, value)
		}
	case AzureDevOps:
		if user, password, ok := r.BasicAuth(); ok {
			return s.basicPrincipal(p, user, password)
		}
		if strings.EqualFold(scheme, "bearer") {
			return s.tokenPrincipal(p, value)
		}
	case BitbucketCloud:
		if strings.EqualFold(scheme, "bearer") {
			return s.tokenPrincipal(p, value)
		}
	case Gitea, Forgejo:
		if strings.EqualFold(scheme, "token") || strings.EqualFold(scheme, "bearer") {
			return s.tokenPrincipal(p, value)
		}
		if user, password, ok := r.BasicAuth(); ok {
			return s.basicPrincipal(p, user, password)
		}
		if token := r.URL.Query().Get("access_token"); token != "" {
			return s.tokenPrincipal(p, token)
		}
	default:
	}
	return principal{}, errUnauthorized
}

// authenticateGit validates git smart HTTP basic auth credentials.
func (s *Server) authenticateGit(p Provider, r *http.Request) (principal, error) {
	user, password, ok := r.BasicAuth()
	if !ok {
		return principal{}, errUnauthorized
	}
	return s.basicPrincipal(p, user, password)
}

// writeUnauthorized writes the provider's response to missing or invalid credentials.
func writeUnauthorized(w http.ResponseWriter, p Provider) {
	switch p {
	case GitHub:
		writeProviderError(w, p, http.StatusUnauthorized, "Bad credentials")
	case GitLab:
		writeProviderError(w, p, http.StatusUnauthorized, "401 Unauthorized")
	case AzureDevOps:
		w.Header().Set("X-Tfs-Fedauthrealm", "Microsoft.TeamFoundation.Server")
		w.Header().Set("WWW-Authenticate", `Basic realm="https://dev.azure.com/"`)
		writeNoContent(w, http.StatusUnauthorized)
	case BitbucketCloud:
		writeProviderError(w, p, http.StatusUnauthorized, "Token is invalid or not supported for this endpoint.")
	default:
		writeProviderError(w, p, http.StatusUnauthorized, "token is required")
	}
}

// RegisterGitHubAppRequest registers a GitHub App and its installations.
type RegisterGitHubAppRequest struct {
	// PublicKeyPEM verifies the App's JWTs (PKIX or PKCS#1 RSA public key, or the private key itself).
	PublicKeyPEM string `json:"publicKeyPEM"`
	// ClientID is the App's client ID, which GitHub accepts as the JWT issuer instead of the App ID. Optional.
	ClientID string `json:"clientID,omitempty"`
	// Installations of the App.
	Installations []GitHubInstallation `json:"installations"`
	// AppID is the GitHub App ID (the JWT issuer).
	AppID int64 `json:"appID"`
}

// GitHubInstallation is an installation of a GitHub App on an account (organization or user).
type GitHubInstallation struct {
	// Account is the login of the organization or user the App is installed on.
	Account string `json:"account"`
	// ID is the installation ID.
	ID int64 `json:"id"`
}

func (s *Server) registerGitHubApp(req RegisterGitHubAppRequest) error {
	if _, err := parseRSAPublicKey([]byte(req.PublicKeyPEM)); err != nil {
		return err
	}
	app := &githubApp{id: req.AppID, clientID: req.ClientID, publicKeyPEM: []byte(req.PublicKeyPEM)}
	for _, inst := range req.Installations {
		app.installations = append(app.installations, githubInstallation{id: inst.ID, account: inst.Account})
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	s.store.apps[req.AppID] = app
	return nil
}

func parseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in public key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 private key: %w", err)
		}
		return &key.PublicKey, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		return &rsaKey.PublicKey, nil
	case "RSA PUBLIC KEY":
		key, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 public key: %w", err)
		}
		return key, nil
	default:
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse public key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
		return rsaKey, nil
	}
}

type jwtClaims struct {
	Iss json.RawMessage `json:"iss"`
	Iat int64           `json:"iat"`
	Exp int64           `json:"exp"`
}

// verifyGitHubJWT validates a GitHub App JWT the way GitHub does and returns the App it was issued by.
func (s *Server) verifyGitHubJWT(token string) (*githubApp, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, "A JSON web token could not be decoded"
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Alg != "RS256" {
		return nil, "A JSON web token could not be decoded"
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	var claims jwtClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	// The issuer is the App ID (as a number or a string) or the App's client ID.
	issuer := strings.Trim(string(claims.Iss), `"`)
	if issuer == "" {
		return nil, "'Issuer' claim ('iss') must be an Integer"
	}
	nowSec := s.now().Unix()
	switch {
	case claims.Exp <= nowSec:
		return nil, "'Expiration time' claim ('exp') must be a numeric value representing the future time at which the assertion expires"
	case claims.Iat > nowSec+60:
		return nil, "'Issued at' claim ('iat') must be an Integer representing the time that the assertion was issued"
	case claims.Exp > nowSec+int64(maxGitHubJWTLifetime.Seconds()):
		// GitHub bounds the expiration relative to its clock, not to iat.
		return nil, "'Expiration time' claim ('exp') is too far in the future"
	default:
	}

	s.store.mu.Lock()
	var app *githubApp
	if appID, err := strconv.ParseInt(issuer, 10, 64); err == nil {
		app = s.store.apps[appID]
	} else {
		for _, candidate := range s.store.apps {
			if candidate.clientID != "" && candidate.clientID == issuer {
				app = candidate
			}
		}
	}
	s.store.mu.Unlock()
	if app == nil {
		return nil, "Integration not found"
	}
	pub, err := parseRSAPublicKey(app.publicKeyPEM)
	if err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, "A JSON web token could not be decoded"
	}
	return app, ""
}
