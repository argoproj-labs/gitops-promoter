package settings

import "sync/atomic"

// clusterScmProviderDisabled is set at startup; the zero value means enabled.
var clusterScmProviderDisabled atomic.Bool

// SetClusterScmProviderEnabled sets whether ClusterScmProvider support is enabled.
func SetClusterScmProviderEnabled(enabled bool) {
	clusterScmProviderDisabled.Store(!enabled)
}

// ClusterScmProviderEnabled reports whether ClusterScmProvider support is enabled.
func ClusterScmProviderEnabled() bool {
	return !clusterScmProviderDisabled.Load()
}
