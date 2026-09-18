package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseImageName(t *testing.T) {
	testCases := []struct {
		image    string
		name     string
		registry string
	}{
		{"nginx", "library/nginx", "docker.io"},
		{"bitnami/kafka", "bitnami/kafka", "docker.io"},
		{"quay.io/prometheus/node-exporter", "prometheus/node-exporter", "quay.io"},
		{"ghcr.io/owner/image", "owner/image", "ghcr.io"},
		{"public.ecr.aws/namespace/image", "namespace/image", "public.ecr.aws"},
	}

	for _, tc := range testCases {
		t.Run(tc.image, func(t *testing.T) {
			name, registry := parseImageName(tc.image)
			assert.Equal(t, tc.name, name)
			assert.Equal(t, tc.registry, registry)
		})
	}
}
