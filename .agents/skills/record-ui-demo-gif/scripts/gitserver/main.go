// Command gitserver runs a smart-HTTP git server on 127.0.0.1:5001 for the dashboard demo GIF.
// The fake SCM provider clones http://localhost:5001/<owner>/<name> and repositories
// are created on first push. See .agents/skills/record-ui-demo-gif/SKILL.md.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sosedoff/gitkit"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dir := flag.String("dir", "/tmp/promoter-ui-demo/git", "directory that holds the bare repositories")
	port := flag.Int("port", 5001, "port to listen on (the fake SCM provider expects 5001)")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o750); err != nil {
		return fmt.Errorf("failed to create git storage dir %q: %w", *dir, err)
	}

	service := gitkit.New(gitkit.Config{Dir: *dir, AutoCreate: true})
	if err := service.Setup(); err != nil {
		return fmt.Errorf("failed to set up gitkit: %w", err)
	}

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           service,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("git server listening on http://%s (storage: %s)", server.Addr, *dir)
	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("git server failed: %w", err)
	}
	return nil
}
