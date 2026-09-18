package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/version"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ImageScanner scans for container images in a Kubernetes cluster.
type ImageScanner struct {
	kubeClient     kubernetes.Interface
	registryClient client.RegistryClient
	logger         logr.Logger
}

// NewImageScanner creates a new image scanner.
func NewImageScanner(kc kubernetes.Interface, rc client.RegistryClient, logger logr.Logger) *ImageScanner {
	return &ImageScanner{
		kubeClient:     kc,
		registryClient: rc,
		logger:         logger,
	}
}

// Name returns the name of the scanner.
func (s *ImageScanner) Name() string {
	return "Image"
}

// Scan discovers container images and checks for updates.
func (s *ImageScanner) Scan(ctx context.Context, opts ScanOptions) ([]types.Resource, error) {
	var resources []types.Resource

	namespaces := opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{corev1.NamespaceAll}
	}

	for _, ns := range namespaces {
		// Get all workloads
		deployments, err := s.kubeClient.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			s.logger.Error(err, "failed to list deployments", "namespace", ns)
		}
		daemonsets, err := s.kubeClient.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			s.logger.Error(err, "failed to list daemonsets", "namespace", ns)
		}
		statefulsets, err := s.kubeClient.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			s.logger.Error(err, "failed to list statefulsets", "namespace", ns)
		}

		// Process workloads
		for _, dep := range deployments.Items {
			resources = append(resources, s.processWorkload(ctx, &dep.Spec.Template.Spec, dep.Namespace, dep.Name)...)
		}
		for _, ds := range daemonsets.Items {
			resources = append(resources, s.processWorkload(ctx, &ds.Spec.Template.Spec, ds.Namespace, ds.Name)...)
		}
		for _, sts := range statefulsets.Items {
			resources = append(resources, s.processWorkload(ctx, &sts.Spec.Template.Spec, sts.Namespace, sts.Name)...)
		}
	}

	return resources, nil
}

func (s *ImageScanner) processWorkload(ctx context.Context, spec *corev1.PodSpec, namespace, ownerName string) []types.Resource {
	var resources []types.Resource
	for _, container := range spec.Containers {
		image := container.Image
		s.logger.V(1).Info("Processing container", "owner", ownerName, "container", container.Name, "image", image)

		imageName, imageIdentifier := parseImage(image)

		if imageName == "" {
			continue
		}

		isDigest := strings.Contains(imageIdentifier, "sha256:")
		s.logger.V(1).Info("Scanning image", "imageName", imageName, "identifier", imageIdentifier, "isDigest", isDigest)

		// Get all available tags for the repository
		tags, err := s.registryClient.GetTags(ctx, imageName)
		if err != nil {
			s.logger.V(1).Info("Failed to get tags for image", "imageName", imageName, "error", err)
			continue
		}
		s.logger.V(1).Info("Found tags for image", "imageName", imageName, "count", len(tags))

		availableVers := version.StringsToVersions(tags)

		var latest, latestMinor, latestPatch *semver.Version
		var updateStatus types.UpdateStatus

		if isDigest {
			// For digests, we don't have a "current" version to compare against.
			// So, we find the absolute latest semver tag in the repository.
			latest, latestMinor, latestPatch = version.Resolve(nil, availableVers) // Pass nil for currentVersion
			if latest != nil {
				updateStatus = types.UpdateStatusDigestUpdateAvailable
			} else {
				updateStatus = types.UpdateStatusPinnedDigest
			}
		} else {
			// For tags, we proceed as before.
			currentVersion, err := version.Parse(imageIdentifier)
			if err != nil {
				s.logger.V(1).Info("Skipping image with non-semver tag", "image", image, "error", err)
				continue
			}
			latest, latestMinor, latestPatch = version.Resolve(currentVersion, availableVers)
			if latest != nil {
				updateStatus = version.Compare(currentVersion, latest)
			} else {
				updateStatus = types.UpdateStatusCurrent
			}
		}

		var latestStr string
		if latest != nil {
			latestStr = latest.String()
		}
		s.logger.V(1).Info("Resolved versions", "imageName", imageName, "latest", latestStr)

		if latest == nil && !isDigest {
			s.logger.V(1).Info("No newer version found for tagged image", "imageName", imageName)
			continue
		}

		// Create the resource, but only if there's an update or it's a digest.
		if latest == nil && !isDigest {
			continue
		}

		res := types.Resource{
			Name:           imageName,
			Namespace:      namespace,
			Type:           types.ResourceTypeImage,
			CurrentVersion: imageIdentifier,
			UpdateStatus:   updateStatus,
		}
		if latest != nil {
			res.LatestVersion = latest.String()
		}
		if latestMinor != nil {
			res.LatestMinor = latestMinor.String()
		}
		if latestPatch != nil {
			res.LatestPatch = latestPatch.String()
		}
		resources = append(resources, res)
	}
	return resources
}

// Validate checks if the scanner can run.
func (s *ImageScanner) Validate(ctx context.Context) error {
	if s.kubeClient == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	if s.registryClient == nil {
		return fmt.Errorf("registry client is not initialized")
	}
	return nil
}

// Cleanup performs any cleanup actions.
func (s *ImageScanner) Cleanup() error {
	return nil
}

// parseImage splits an image string into its name and tag/digest.
func parseImage(image string) (name, tagOrDigest string) {
	if strings.Contains(image, "@sha256:") {
		parts := strings.Split(image, "@")
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}

	lastColon := strings.LastIndex(image, ":")
	lastSlash := strings.LastIndex(image, "/")

	if lastColon > lastSlash {
		// Check if the part after the colon is a digest
		if strings.HasPrefix(image[lastColon+1:], "sha256:") {
			return "", "" // Should have been caught by the first check
		}
		return image[:lastColon], image[lastColon+1:]
	}

	return image, "latest"
}
