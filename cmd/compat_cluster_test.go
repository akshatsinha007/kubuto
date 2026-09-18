package cmd

import (
	"testing"

	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/stretchr/testify/assert"
)

// TestApplyClusterCompatResult_PropagatesStaleAndSource guards a real
// regression: compat_cluster.go previously never set CompatStale/
// CompatSource on the resources it processes, even though the engine
// returns both and compat chart/compat release both already surface
// them via displayCompatResult.
func TestApplyClusterCompatResult_PropagatesStaleAndSource(t *testing.T) {
	res := &types.Resource{Name: "ingress-nginx", CurrentVersion: "4.10.0"}
	resp := &engine.CompatResponse{
		Compatible: true,
		Status:     "found",
		Source:     "https://kubernetes.github.io/ingress-nginx/",
		IsStale:    true,
	}

	breaking := applyClusterCompatResult(res, resp, "1.28")

	assert.False(t, breaking)
	assert.Equal(t, types.CompatStatusCompatible, res.CompatStatus)
	assert.True(t, res.CompatStale, "is_stale must propagate, not be silently dropped")
	assert.Equal(t, "https://kubernetes.github.io/ingress-nginx/", res.CompatSource)
	assert.Equal(t, "compatible", res.MigrationAction)
}

func TestApplyClusterCompatResult_IncompatibleTracksMigrationAction(t *testing.T) {
	res := &types.Resource{Name: "old-chart", CurrentVersion: "1.0.0"}
	resp := &engine.CompatResponse{Compatible: false, Status: "found"}

	breaking := applyClusterCompatResult(res, resp, "1.31")

	assert.True(t, breaking)
	assert.Equal(t, types.CompatStatusIncompatible, res.CompatStatus)
	assert.Equal(t, "incompatible with K8s 1.31", res.MigrationAction)
}

func TestApplyClusterCompatResult_NotTrackedMigrationAction(t *testing.T) {
	res := &types.Resource{Name: "obscure", CurrentVersion: "1.0.0"}
	resp := &engine.CompatResponse{Compatible: false, Status: "not_tracked"}

	breaking := applyClusterCompatResult(res, resp, "1.31")

	// Matches the pre-existing behavior this function preserves: any
	// !Compatible response marks the resource Incompatible — only the
	// MigrationAction text distinguishes no_data/not_tracked/other.
	assert.True(t, breaking)
	assert.Equal(t, types.CompatStatusIncompatible, res.CompatStatus)
	assert.Contains(t, res.MigrationAction, "not tracked")
}
