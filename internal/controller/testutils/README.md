# Test Utilities

## GitHTTPServer

Git Smart HTTP server for tests. Supports both SHA-1 and SHA-256 repositories.

### Usage

```go
server := &testutils.GitHTTPServer{
    RepoDir:      "/tmp/repos",
    ObjectFormat: "sha256",  // or "sha1" (default)
    AutoCreate:   true,
}
```

Set `ObjectFormat` to control hash format for auto-created repos.

Used by:
- Main test suite (`suite_test.go`) - SHA-1 default
- SHA-256 test - Dedicated instance with SHA-256
