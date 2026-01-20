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

package controller

import (
	"context"
	"strings"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

// OnlyLocalCluster creates a handler that only processes events from the local cluster.
// The local cluster is identified by the mcmanager.LocalCluster constant.
func OnlyLocalCluster() mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		// LocalCluster is represented by an empty string
		if clusterName != "" {
			return &noOpHandler{}
		}
		return mchandler.Lift(&handler.EnqueueRequestForObject{})(clusterName, cl)
	}
}

// OnlyProviderClusters creates a handler that only processes events from provider clusters (not local).
func OnlyProviderClusters() mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		// Filter OUT local cluster (empty string means local)
		if clusterName == "" {
			return &noOpHandler{}
		}
		// Enqueue for all provider clusters
		return mchandler.Lift(&handler.EnqueueRequestForObject{})(clusterName, cl)
	}
}

// FilterByPrefix creates a handler that only processes events from clusters
// matching the given provider prefix.
func FilterByPrefix(prefix string) mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		if !strings.HasPrefix(clusterName, prefix+"#") {
			return &noOpHandler{}
		}
		return mchandler.Lift(&handler.EnqueueRequestForObject{})(clusterName, cl)
	}
}

// FilterByPrefixes creates a handler that only processes events from clusters
// matching any of the given provider prefixes.
func FilterByPrefixes(prefixes ...string) mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		for _, prefix := range prefixes {
			if strings.HasPrefix(clusterName, prefix+"#") {
				return mchandler.Lift(&handler.EnqueueRequestForObject{})(clusterName, cl)
			}
		}
		return &noOpHandler{}
	}
}

// MaybeLocalOrProviderClusters creates a handler that processes events from local cluster
// (if watchLocal is true) and/or provider clusters (if watchProviders is true).
func MaybeLocalOrProviderClusters(watchLocal bool, watchProviders bool) mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		isLocal := clusterName == ""

		if isLocal && !watchLocal {
			return &noOpHandler{}
		}

		if !isLocal && !watchProviders {
			return &noOpHandler{}
		}

		return mchandler.Lift(&handler.EnqueueRequestForObject{})(clusterName, cl)
	}
}

// noOpHandler drops all events
type noOpHandler struct{}

func (h *noOpHandler) Create(ctx context.Context, evt event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
}

func (h *noOpHandler) Update(ctx context.Context, evt event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
}

func (h *noOpHandler) Delete(ctx context.Context, evt event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
}

func (h *noOpHandler) Generic(ctx context.Context, evt event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[mcreconcile.Request]) {
}
