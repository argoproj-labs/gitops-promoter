/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//nolint:gofumpt // Test utility file, minor formatting differences acceptable
package testutils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// GitHTTPServer implements a simple Git Smart HTTP server using git http-backend.
// Unlike gitkit, this supports SHA-256 repositories by allowing configuration of
// the object format used when auto-creating repositories.
type GitHTTPServer struct {
	Logger       *log.Logger
	RepoDir      string // Base directory for repositories
	ObjectFormat string // "sha1" or "sha256" - format for auto-created repos
	AutoCreate   bool   // Automatically create repositories if they don't exist
}

var reSlashDedup = regexp.MustCompile(`/{2,}`)

// ServeHTTP implements http.Handler for the Git Smart HTTP protocol
func (s *GitHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.logf("request: %s %s", r.Method, r.URL.Path)

	// Parse repository path from URL
	repoName := s.parseRepoPath(r.URL.Path)
	if repoName == "" {
		http.Error(w, "Bad Request: no repository specified", http.StatusBadRequest)
		return
	}

	repoPath := filepath.Join(s.RepoDir, repoName)

	// Auto-create repository if it doesn't exist
	if s.AutoCreate && !s.repoExists(repoPath) {
		if err := s.initRepo(r.Context(), repoPath); err != nil {
			s.logf("failed to init repo %s: %v", repoName, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		s.logf("auto-created repository: %s (format: %s)", repoName, s.ObjectFormat)
	}

	// Check if repository exists
	if !s.repoExists(repoPath) {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	// Determine which Git service is being requested
	service := r.URL.Query().Get("service")

	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/info/refs") {
		s.serveInfoRefs(r.Context(), w, r, repoPath, service)
		return
	}

	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-upload-pack") {
		s.serveUploadPack(r.Context(), w, r, repoPath)
		return
	}

	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-receive-pack") {
		s.serveReceivePack(r.Context(), w, r, repoPath)
		return
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

// serveInfoRefs handles GET requests for /info/refs (capability advertisement)
func (s *GitHTTPServer) serveInfoRefs(ctx context.Context, w http.ResponseWriter, _ *http.Request, repoPath, service string) {
	if service != "git-upload-pack" && service != "git-receive-pack" {
		http.Error(w, "Invalid service", http.StatusBadRequest)
		return
	}

	// Run git upload-pack or receive-pack with --advertise-refs
	cmdName := strings.TrimPrefix(service, "git-")
	cmd := exec.CommandContext(ctx, "git", cmdName, "--stateless-rpc", "--advertise-refs", repoPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := cmd.Start(); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Set headers
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Write packet-line formatted service announcement
	_, _ = fmt.Fprintf(w, "%04x# service=%s\n", len(service)+15, service)
	_, _ = fmt.Fprint(w, "0000") // flush packet

	// Stream git output
	_, _ = io.Copy(w, stdout)
	_ = cmd.Wait()
}

func (s *GitHTTPServer) serveUploadPack(ctx context.Context, w http.ResponseWriter, r *http.Request, repoPath string) {
	s.serveRPC(ctx, w, r, repoPath, "upload-pack")
}

func (s *GitHTTPServer) serveReceivePack(ctx context.Context, w http.ResponseWriter, r *http.Request, repoPath string) {
	s.serveRPC(ctx, w, r, repoPath, "receive-pack")
}

func (s *GitHTTPServer) serveRPC(ctx context.Context, w http.ResponseWriter, r *http.Request, repoPath, rpc string) {
	body := r.Body

	cmd := exec.CommandContext(ctx, "git", rpc, "--stateless-rpc", repoPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		s.logf("failed to start git %s: %v", rpc, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Copy request body to git stdin
	go func() {
		_, _ = io.Copy(stdin, body)
		_ = stdin.Close()
	}()

	// Set response headers
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-result", rpc))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Stream git output to response
	_, _ = io.Copy(w, stdout)

	if err := cmd.Wait(); err != nil {
		s.logf("git %s failed: %v, stderr: %s", rpc, err, stderr.String())
	}
}

func (s *GitHTTPServer) initRepo(ctx context.Context, repoPath string) error {
	args := []string{"init", "--bare"}

	// Add object format if specified
	if s.ObjectFormat == "sha256" {
		args = append(args, "--object-format=sha256")
	}

	args = append(args, repoPath)

	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init failed: %w, output: %s", err, string(output))
	}

	return nil
}

// repoExists checks if a Git repository exists at the given path
func (s *GitHTTPServer) repoExists(repoPath string) bool {
	// Check for objects directory (present in all git repos)
	_, err := os.Stat(filepath.Join(repoPath, "objects"))
	return err == nil
}

// parseRepoPath extracts the repository name from the URL path
// Examples:
//
//	/repo.git/info/refs -> repo.git
//	/org/repo.git/git-upload-pack -> org/repo.git
func (s *GitHTTPServer) parseRepoPath(urlPath string) string {
	// Remove duplicate slashes
	urlPath = reSlashDedup.ReplaceAllString(urlPath, "/")

	// Remove leading slash
	urlPath = strings.TrimPrefix(urlPath, "/")

	// Remove Git service suffixes
	urlPath = strings.TrimSuffix(urlPath, "/info/refs")
	urlPath = strings.TrimSuffix(urlPath, "/git-upload-pack")
	urlPath = strings.TrimSuffix(urlPath, "/git-receive-pack")

	return urlPath
}

// logf logs a message if a logger is configured
func (s *GitHTTPServer) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
	}
}

// Setup ensures the repository directory exists
func (s *GitHTTPServer) Setup() error {
	if s.ObjectFormat == "" {
		s.ObjectFormat = "sha1" // Default to SHA-1 for backward compatibility
	}

	if err := os.MkdirAll(s.RepoDir, 0755); err != nil {
		return fmt.Errorf("failed to create repo directory: %w", err)
	}

	return nil
}
