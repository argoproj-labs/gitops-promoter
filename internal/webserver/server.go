package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	ginlogr "github.com/argoproj-labs/gitops-promoter/internal/webserver/logr"
	"github.com/argoproj-labs/gitops-promoter/ui/web"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var logger = ctrl.Log.WithName("webServer")

// WebServer handles the web server functionality for the dashboard and API endpoints.
type WebServer struct {
	client.Client
	Scheme *runtime.Scheme
	Event  *Event
}

// Event represents a server-sent event that can be broadcast to clients.
// It keeps a list of clients those are currently attached
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

// Message represents a message that can be sent to clients.
type Message struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
}

// ClientChan represents a channel for sending messages to clients.
// New event messages are broadcast to all registered client connection channels
type ClientChan chan Message

// NewWebServer creates a new WebServer instance with the given manager.
func NewWebServer(mgr controllerruntime.Manager) WebServer {
	event := &Event{
		Message:       make(chan Message, 100),
		newClients:    make(chan chan Message),
		closedClients: make(chan chan Message),
		totalClients:  make(map[chan Message]bool),
	}
	go event.listen()

	return WebServer{
		Event:  event,
		Scheme: mgr.GetScheme(),
		Client: mgr.GetClient(),
	}
}

// Reconcile handles the reconciliation logic for the WebServer controller.
func (ws *WebServer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (ws *WebServer) sendEvent(e client.Object) {
	annotations := e.GetAnnotations()
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	e.SetAnnotations(annotations)
	e.SetManagedFields(nil)

	jsonString, err := json.Marshal(e)
	if err != nil {
		logger.Error(err, "failed to marshal for SSE", "name", e.GetName(), "kind", e.GetObjectKind().GroupVersionKind().Kind)
		return
	}
	m := Message{
		Name:      e.GetName(),
		Namespace: e.GetNamespace(),
		Kind:      e.GetObjectKind().GroupVersionKind().Kind,
		Data:      string(jsonString),
	}
	ws.Event.Message <- m
}

func (ws *WebServer) sendDeleteEvent(e client.Object) {
	ws.Event.Message <- Message{
		Name:      e.GetName(),
		Namespace: e.GetNamespace(),
		Kind:      e.GetObjectKind().GroupVersionKind().Kind,
		Data:      "{action: \"delete\", name: \"" + e.GetName() + "\", namespace: \"" + e.GetNamespace() + "\"}",
	}
}

// SetupWithManager sets up the WebServer controller with the given manager.
func (ws *WebServer) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		Named("webServer").
		Watches(&promoterv1alpha1.PromotionStrategy{}, handler.Funcs{ //nolint:dupl
			CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ps, ok := e.Object.(*promoterv1alpha1.PromotionStrategy); ok {
					ps.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PromotionStrategy"))
					ws.sendEvent(ps)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ps, ok := e.ObjectNew.(*promoterv1alpha1.PromotionStrategy); ok {
					ps.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PromotionStrategy"))
					ws.sendEvent(ps)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ps, ok := e.Object.(*promoterv1alpha1.PromotionStrategy); ok {
					ps.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PromotionStrategy"))
					ws.sendDeleteEvent(ps)
				}
			},
		}).
		Watches(&promoterv1alpha1.ChangeTransferPolicy{}, handler.Funcs{ //nolint:dupl
			CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ctp, ok := e.Object.(*promoterv1alpha1.ChangeTransferPolicy); ok {
					ctp.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("ChangeTransferPolicy"))
					ws.sendEvent(ctp)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ctp, ok := e.ObjectNew.(*promoterv1alpha1.ChangeTransferPolicy); ok {
					ctp.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("ChangeTransferPolicy"))
					ws.sendEvent(ctp)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if ctp, ok := e.Object.(*promoterv1alpha1.ChangeTransferPolicy); ok {
					ctp.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("ChangeTransferPolicy"))
					ws.sendDeleteEvent(ctp)
				}
			},
		}).
		Watches(&promoterv1alpha1.PullRequest{}, handler.Funcs{ //nolint:dupl
			CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if pr, ok := e.Object.(*promoterv1alpha1.PullRequest); ok {
					pr.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PullRequest"))
					ws.sendEvent(pr)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if pr, ok := e.ObjectNew.(*promoterv1alpha1.PullRequest); ok {
					pr.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PullRequest"))
					ws.sendEvent(pr)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if pr, ok := e.Object.(*promoterv1alpha1.PullRequest); ok {
					pr.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("PullRequest"))
					ws.sendDeleteEvent(pr)
				}
			},
		}).
		Watches(&promoterv1alpha1.CommitStatus{}, handler.Funcs{ //nolint:dupl
			CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if cs, ok := e.Object.(*promoterv1alpha1.CommitStatus); ok {
					cs.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("CommitStatus"))
					ws.sendEvent(cs)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if cs, ok := e.ObjectNew.(*promoterv1alpha1.CommitStatus); ok {
					cs.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("CommitStatus"))
					ws.sendEvent(cs)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[client.Object], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if cs, ok := e.Object.(*promoterv1alpha1.CommitStatus); ok {
					cs.SetGroupVersionKind(promoterv1alpha1.GroupVersion.WithKind("CommitStatus"))
					ws.sendDeleteEvent(cs)
				}
			},
		}).
		Complete(ws)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// StartDashboard starts the dashboard web server on the specified address.
func (ws *WebServer) StartDashboard(ctx context.Context, addr string) error {
	// Embed dashboard UI from DashboardFS
	distFS, err := fs.Sub(web.DashboardFS, "static")
	if err != nil {
		return fmt.Errorf("failed to create sub FS: %w", err)
	}

	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		return fmt.Errorf("failed to create assets sub FS: %w", err)
	}

	router := gin.New()
	router.Use(ginlogr.Ginlogr(logger, time.RFC3339, true))
	router.Use(ginlogr.RecoveryWithLogr(logger, time.RFC3339, true, true))

	router.GET("/watch", WatchHeadersMiddleware(), ws.Event.serveHTTP(), ws.httpWatch)

	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.GET("/list", ws.httpList)

	router.GET("/get", ws.httpGet)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, "ok")
	})

	// Handle favicon
	router.GET("/favicon.ico", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	// Serve static files from embed
	router.StaticFS("/assets", http.FS(assetsFS))
	router.GET("/", func(c *gin.Context) {
		f, err := distFS.Open("index.html")
		if err != nil {
			c.String(500, "Failed to open index.html: %v", err)
			return
		}
		defer func() {
			cerr := f.Close()
			if cerr != nil {
				logger.Error(cerr, "failed to close file")
			}
		}()
		content, err := io.ReadAll(f)
		if err != nil {
			c.String(500, "Failed to read index.html: %v", err)
			return
		}
		c.Data(200, "text/html; charset=utf-8", content)
	})

	// SPA route handler for nested routes
	router.GET("/:section/*path", func(c *gin.Context) {
		section := c.Param("section")

		// Skip if it's an API or static asset
		if section == "watch" || section == "list" || section == "get" || section == "healthz" ||
			section == "assets" {
			c.Status(http.StatusNotFound)
			return
		}
		// Serve index.html for SPA routes
		f, err := distFS.Open("index.html")
		if err != nil {
			c.String(500, "Failed to open index.html: %v", err)
			return
		}
		defer func() {
			cerr := f.Close()
			if cerr != nil {
				logger.Error(cerr, "failed to close file")
			}
		}()
		content, err := io.ReadAll(f)
		if err != nil {
			c.String(500, "Failed to read index.html: %v", err)
			return
		}
		c.Data(200, "text/html; charset=utf-8", content)
	})

	// Serve index.html for all other routes from embed
	router.NoRoute(func(c *gin.Context) {
		c.FileFromFS("index.html", http.FS(distFS))
	})

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

func (ws *WebServer) httpGet(c *gin.Context) {
	if c.Query("kind") == "" {
		c.JSON(http.StatusBadRequest, "kind is required")
		return
	}
	if c.Query("namespace") == "" {
		c.JSON(http.StatusBadRequest, "namespace is required")
		return
	}
	if c.Query("name") == "" {
		c.JSON(http.StatusBadRequest, "name is required")
		return
	}
	kind := strings.ToLower(c.Query("kind"))
	namespace := strings.ToLower(c.Query("namespace"))
	name := strings.ToLower(c.Query("name"))

	switch kind {
	case "promotionstrategy":
		ps := &promoterv1alpha1.PromotionStrategy{}
		err := ws.Get(c, client.ObjectKey{Namespace: namespace, Name: name}, ps)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, ps)

	case "changetransferpolicy":
		ctps := &promoterv1alpha1.ChangeTransferPolicy{}
		err := ws.Get(c, client.ObjectKey{Namespace: namespace, Name: name}, ctps)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, ctps)

	case "pullrequest":
		pr := &promoterv1alpha1.PullRequest{}
		err := ws.Get(c, client.ObjectKey{Namespace: namespace, Name: name}, pr)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, pr)

	case "commitstatus":
		cs := &promoterv1alpha1.CommitStatus{}
		err := ws.Get(c, client.ObjectKey{Namespace: namespace, Name: name}, cs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, cs)

	default:
		c.JSON(http.StatusBadRequest, "invalid kind")
	}
}

func (ws *WebServer) httpList(c *gin.Context) {
	if c.Query("kind") == "" {
		c.JSON(http.StatusBadRequest, "kind is required")
		return
	}
	kind := strings.ToLower(c.Query("kind"))
	listOptions := &client.ListOptions{}
	if c.Query("namespace") != "" {
		listOptions = &client.ListOptions{Namespace: c.Query("namespace")}
	}

	switch kind {
	case "promotionstrategy":
		psl := &promoterv1alpha1.PromotionStrategyList{}
		err := ws.List(c, psl, listOptions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, psl.Items)

	case "changetransferpolicy":
		ctpl := &promoterv1alpha1.ChangeTransferPolicyList{}
		err := ws.List(c, ctpl, listOptions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, ctpl.Items)

	case "pullrequest":
		prl := &promoterv1alpha1.PullRequestList{}
		err := ws.List(c, prl, listOptions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, prl.Items)

	case "commitstatus":
		csl := &promoterv1alpha1.CommitStatusList{}
		err := ws.List(c, csl, listOptions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, csl.Items)

	case "namespace":
		if c.Query("namespace") != "" {
			c.JSON(http.StatusBadRequest, "namespace is not valid for listing namespaces")
			return
		}

		m := make(map[string]bool)
		var namespaces []string

		psl := &promoterv1alpha1.PromotionStrategyList{}
		err := ws.List(c, psl, &client.ListOptions{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		for _, ps := range psl.Items {
			ns := ps.Namespace
			if !m[ns] {
				m[ns] = true
				namespaces = append(namespaces, ns)
			}
		}
		c.JSON(http.StatusOK, namespaces)

	default:
		c.JSON(http.StatusBadRequest, "invalid kind")
	}
}

func (ws *WebServer) httpWatch(c *gin.Context) {
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
			match, err := filter(msg, c)
			if err != nil {
				logger.Error(err, "failed to filter message", "name", msg.Name, "kind", msg.Kind)
				return false
			}

			if match {
				c.SSEvent(msg.Kind, msg.Data)
				c.Writer.Flush()
			}

			return true
		}
		return false
	})
	if gone {
		logger.Info("client gone stream")
		// Send closed connection to event server
		ws.Event.closedClients <- clientChan
	}
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

// WatchHeadersMiddleware returns a gin middleware that sets headers for watch requests.
func WatchHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Next()
	}
}

func filter(msg Message, c *gin.Context) (bool, error) {
	msgKey := fmt.Sprintf("%s/%s/%s", msg.Kind, msg.Namespace, msg.Name)
	msgKey = strings.ToLower(msgKey)
	var kindQuery, namespaceQuery, nameQuery string
	if kindQuery = c.Query("kind"); kindQuery == "" {
		kindQuery = "*"
	}
	if namespaceQuery = c.Query("namespace"); namespaceQuery == "" {
		namespaceQuery = "*"
	}
	if nameQuery = c.Query("name"); nameQuery == "" {
		nameQuery = "*"
	}
	queryKey := fmt.Sprintf("%s/%s/%s", strings.ToLower(kindQuery), strings.ToLower(namespaceQuery), strings.ToLower(nameQuery))

	match, err := path.Match(queryKey, msgKey)
	logger.V(1).Info("filter", "msgKey", msgKey, "queryKey", queryKey, "match", match)
	if err != nil {
		return false, fmt.Errorf("failed to match path: %w", err)
	}
	return match, nil
}
