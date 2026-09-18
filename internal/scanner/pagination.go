package scanner

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// defaultListPageSize bounds every paginated List() call in this package.
// Without it, a single List() against a cluster with thousands of Helm
// release secrets, ArgoCD Applications, or Flux HelmReleases returns
// however many objects the API server's own default page size allows in
// one response — untested, and a risk this package previously carried on
// every List() call site (all were unbounded, single-shot calls with no
// Continue-token handling at all).
const defaultListPageSize = 500

// paginatedSecretList lists every Secret matching listOpts across all
// pages, following the API server's continuation token until exhausted.
// `list` is a client-go typed List method value (e.g.
// `kubeClient.CoreV1().Secrets(ns).List`) — accepting the method directly
// rather than the whole SecretInterface keeps call sites a one-line
// change and keeps this function trivially testable with a hand-written
// closure, no clientset required.
func paginatedSecretList(ctx context.Context, list func(context.Context, metav1.ListOptions) (*corev1.SecretList, error), listOpts metav1.ListOptions) ([]corev1.Secret, error) {
	if listOpts.Limit == 0 {
		listOpts.Limit = defaultListPageSize
	}

	var all []corev1.Secret
	for {
		page, err := list(ctx, listOpts)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.Continue == "" {
			break
		}
		listOpts.Continue = page.Continue
	}
	return all, nil
}

// paginatedUnstructuredList is `paginatedSecretList`'s counterpart for
// dynamic-client-based List() calls (ArgoCD Applications, Flux
// HelmReleases/HelmRepositories) — same continuation-token loop, against
// `*unstructured.UnstructuredList` instead of a typed list.
func paginatedUnstructuredList(ctx context.Context, list func(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error), listOpts metav1.ListOptions) ([]unstructured.Unstructured, error) {
	if listOpts.Limit == 0 {
		listOpts.Limit = defaultListPageSize
	}

	var all []unstructured.Unstructured
	for {
		page, err := list(ctx, listOpts)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		cont := page.GetContinue()
		if cont == "" {
			break
		}
		listOpts.Continue = cont
	}
	return all, nil
}
