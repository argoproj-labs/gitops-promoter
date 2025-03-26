package webserver

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
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
	NewClients chan chan Message

	// Closed client connections
	ClosedClients chan chan Message

	// Total client connections
	TotalClients map[chan Message]bool
}

type Message struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// New event messages are broadcast to all registered client connection channels
type ClientChan chan Message

func NewWebServer(mgr controllerruntime.Manager) WebServer {
	event := &Event{
		Message:       make(chan Message),
		NewClients:    make(chan chan Message),
		ClosedClients: make(chan chan Message),
		TotalClients:  make(map[chan Message]bool),
	}
	go event.listen()

	return WebServer{
		Event:     event,
		mgr:       mgr,
		k8sClient: mgr.GetClient(),
	}
}

func (wr *WebServer) Start(ctx context.Context, addr string) error {
	router := gin.Default()

	router.GET("/stream", HeadersMiddleware(), wr.Event.serveHTTP(), func(c *gin.Context) {
		v, ok := c.Get("clientChan")
		if !ok {
			return
		}
		clientChan, ok := v.(ClientChan)
		if !ok {
			return
		}

		go func() {
			//<-c.Writer.CloseNotify()

			<-c.Request.Context().Done()

			// Drain client channel so that it does not block. Server may keep sending messages to this channel
			for range clientChan {
			}
			// Send closed connection to event server
			wr.Event.ClosedClients <- clientChan
		}()

		//namespace := c.Query("namespace")

		gone := c.Stream(func(w io.Writer) bool {
			// Stream message to client from message channel
			if msg, ok := <-clientChan; ok {
				c.SSEvent(msg.Name, msg.Data)
				return true
			}
			return false
		})
		if gone {
			logger.Info("client gone stream")
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
		case client := <-stream.NewClients:
			stream.TotalClients[client] = true
			logger.Info("Client added.", "clientCount", len(stream.TotalClients))

		// Remove closed client
		case client := <-stream.ClosedClients:
			delete(stream.TotalClients, client)
			close(client)
			logger.Info("Removed client.", "clientCount", len(stream.TotalClients))

		// Broadcast message to client
		case eventMsg := <-stream.Message:
			for clientMessageChan := range stream.TotalClients {
				select {
				case clientMessageChan <- eventMsg:
					// Message sent successfully
					logger.Info("Sent.", "clientCount", len(stream.TotalClients), "message", eventMsg, "clientMessageChan", clientMessageChan)
				default:
					// Failed to send, dropping message
					logger.Info("Failed to send.", "clientCount", len(stream.TotalClients))
					//stream.ClosedClients <- clientMessageChan
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
		stream.NewClients <- clientChan

		//go func() {
		//	//<-c.Writer.CloseNotify()
		//	<-c.Request.Context().Done()
		//
		//	// Drain client channel so that it does not block. Server may keep sending messages to this channel
		//	for range clientChan {
		//	}
		//	// Send closed connection to event server
		//	stream.ClosedClients <- clientChan
		//}()

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
		c.Next()
	}
}
