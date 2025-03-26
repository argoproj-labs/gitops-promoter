// Package ginlogr provides log handling using logr package.
// Code structure based on ginrus package.
package ginlogr

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
)

// Ginlogr returns a gin.HandlerFunc (middleware) that logs requests using github.com/go-logr/logr.
//
// Requests with errors are logged using logr.Error().
// Requests without errors are logged using logr.Info().
//
// It receives:
//  1. A time package format string (e.g. time.RFC3339).
//  2. A boolean stating whether to use UTC time zone or local.
func Ginlogr(logger logr.Logger, timeFormat string, utc bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		// some evil middlewares modify this values
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		c.Next()

		end := time.Now()
		latency := end.Sub(start)
		if utc {
			end = end.UTC()
		}

		if len(c.Errors) > 0 {
			// Append error field if this is an erroneous request.
			for _, e := range c.Errors.Errors() {
				logger.Error(errors.New(e), "Error")
			}
		} else {
			logger.Info(path,
				"status", c.Writer.Status(),
				"method", c.Request.Method,
				"path", path,
				"query", query,
				"ip", c.ClientIP(),
				"user-agent", c.Request.UserAgent(),
				"time", end.Format(timeFormat),
				"latency", latency,
			)
		}
	}
}

// RecoveryWithlogr returns a gin.HandlerFunc (middleware)
// that recovers from any panics and logs requests using uber-go/logr.
// All errors are logged using logr.Error().
// stack means whether output the stack info.
// The stack info is easy to find where the error occurs but the stack info is too large.
func RecoveryWithLogr(logger logr.Logger, timeFormat string, utc, stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				time := time.Now()
				if utc {
					time = time.UTC()
				}

				// Check for a broken connection, as it is not really a
				// condition that warrants a panic stack trace.
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") ||
							strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				httpRequest, _ := httputil.DumpRequest(c.Request, false)

				e, ok := err.(error)
				if !ok {
					e = fmt.Errorf("%v", err)
				}

				switch {
				case brokenPipe:
					logger.Error(err.(*os.SyscallError), c.Request.URL.Path,
						"time", time.Format(timeFormat),
						"request", string(httpRequest),
					)
					// If the connection is dead, we can't write a status to it.
					c.Error(e) // nolint: errcheck
					c.Abort()
					return
				case stack:
					logger.Error(e, "[Recovery from panic]",
						"time", time.Format(timeFormat),
						"request", string(httpRequest),
						"stack", string(debug.Stack()),
					)
				default:
					logger.Error(e, "[Recovery from panic]",
						"time", time.Format(timeFormat),
						"request", string(httpRequest),
					)
				}

				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
