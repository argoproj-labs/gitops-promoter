package webserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	ginlogr "github.com/argoproj-labs/gitops-promoter/internal/webserver/logr"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
)

var logger = ctrl.Log.WithName("webServer")

type WebServer struct {
	Event     *Event
	mgr       controllerruntime.Manager
	k8sClient client.Client
}

// It keeps a list of clients those are currently attached
// and broadcasting events to those clients.
type Event struct {
	// Events are pushed to this channel by the main events-gathering routine
	Message chan Message

	// New client connections
	newClients chan chan Message

	// Closed client connections
	closedClients chan chan Message

	// Total client connections
	totalClients map[chan Message]bool
}

type Message struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
}

// New event messages are broadcast to all registered client connection channels
type ClientChan chan Message

func NewWebServer(mgr controllerruntime.Manager) WebServer {
	event := &Event{
		Message:       make(chan Message, 100),
		newClients:    make(chan chan Message),
		closedClients: make(chan chan Message),
		totalClients:  make(map[chan Message]bool),
	}
	go event.listen()

	return WebServer{
		Event:     event,
		mgr:       mgr,
		k8sClient: mgr.GetClient(),
	}
}

func (wr *WebServer) Start(ctx context.Context, addr string) error {
	router := gin.New()
	router.Use(ginlogr.Ginlogr(logger, time.RFC3339, true))
	router.Use(ginlogr.RecoveryWithLogr(logger, time.RFC3339, true, true))
	router.Use(gzip.Gzip(gzip.DefaultCompression))

	router.GET("/stream", HeadersMiddleware(), wr.Event.serveHTTP(), func(c *gin.Context) {
		v, ok := c.Get("clientChan")
		if !ok {
			return
		}
		clientChan, ok := v.(ClientChan)
		if !ok {
			return
		}

		gone := c.Stream(func(w io.Writer) bool {
			// Stream message to client from message channel
			if msg, ok := <-clientChan; ok {
				if c.Query("namespace") != "" {
					// Filter message by namespace
					if msg.Namespace == c.Query("namespace") {
						c.SSEvent(msg.Kind, msg.Data)
					}
				} else {
					c.SSEvent(msg.Kind, msg.Data)
				}
				return true
			}
			return false
		})
		if gone {
			logger.Info("client gone stream")
			// Send closed connection to event server
			wr.Event.closedClients <- clientChan
		}
	})

	// Parse Static files
	router.StaticFile("/", "./public/index.html")

	server := http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("web server closed")
		} else if err != nil {
			logger.Error(err, "error listening for server")
		}
	}()

	logger.Info("web server started")

	<-ctx.Done()
	logger.Info("web server stopped")

	if err := server.Shutdown(ctx); err != nil {
		logger.Error(err, "web server shutdown failed", "error", err)
	}
	logger.Info("web server exited properly")

	return nil
}

// It Listens all incoming requests from clients.
// Handles addition and removal of clients and broadcast messages to clients.
func (stream *Event) listen() {
	for {
		select {
		// Add new available client
		case client := <-stream.newClients:
			stream.totalClients[client] = true
			logger.Info("Client added.", "clientCount", len(stream.totalClients))

		// Remove closed client
		case client := <-stream.closedClients:
			delete(stream.totalClients, client)
			close(client)
			logger.Info("Removed client.", "clientCount", len(stream.totalClients))

		// Broadcast message to client
		case eventMsg := <-stream.Message:
			for clientMessageChan := range stream.totalClients {
				select {
				case clientMessageChan <- eventMsg:
					// Message sent successfully
				default:
					// Failed to send, dropping message
					logger.Info("Failed to send.", "clientCount", len(stream.totalClients))
				}
			}
		}
	}
}

func (stream *Event) serveHTTP() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Initialize client channel
		clientChan := make(ClientChan)

		// Send new connection to event server
		stream.newClients <- clientChan

		c.Set("clientChan", clientChan)

		c.Next()
	}
}

func HeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		// c.Writer.Header().Set("Content-Encoding", "deflate")
		c.Next()
	}
}
