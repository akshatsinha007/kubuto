package scanner

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These tests exercise paginatedSecretList/paginatedUnstructuredList
// against hand-scripted closures that simulate a real API server's
// multi-page behavior (each page returns a Continue token until the last
// one). This is deliberate: k8s.io/client-go/kubernetes/fake's List()
// ignores Limit/Continue entirely and always returns every matching
// object in a single page (confirmed empirically before writing this —
// see the pagination.go commit message), so a test built on the fake
// clientset would pass even if the pagination loop were deleted entirely.
// Only a script we control can actually prove the loop pages correctly.

func TestPaginatedSecretList_FollowsContinueAcrossPages(t *testing.T) {
	pages := [][]corev1.Secret{
		{{ObjectMeta: metav1.ObjectMeta{Name: "a"}}, {ObjectMeta: metav1.ObjectMeta{Name: "b"}}},
		{{ObjectMeta: metav1.ObjectMeta{Name: "c"}}, {ObjectMeta: metav1.ObjectMeta{Name: "d"}}},
		{{ObjectMeta: metav1.ObjectMeta{Name: "e"}}},
	}
	var calls []metav1.ListOptions
	list := func(_ context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
		calls = append(calls, opts)
		i := len(calls) - 1
		if i >= len(pages) {
			t.Fatalf("list called more times than there are pages (call %d)", i)
		}
		cont := ""
		if i < len(pages)-1 {
			cont = "page-token-" + string(rune('1'+i))
		}
		return &corev1.SecretList{
			ListMeta: metav1.ListMeta{Continue: cont},
			Items:    pages[i],
		}, nil
	}

	got, err := paginatedSecretList(context.Background(), list, metav1.ListOptions{LabelSelector: "owner=helm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 accumulated items across 3 pages, got %d", len(got))
	}
	if len(calls) != 3 {
		t.Fatalf("expected exactly 3 List() calls (one per page), got %d", len(calls))
	}
	// The label selector must survive across every page, and each call
	// after the first must carry the previous page's Continue token.
	for i, c := range calls {
		if c.LabelSelector != "owner=helm" {
			t.Errorf("call %d: LabelSelector changed to %q", i, c.LabelSelector)
		}
		if i == 0 {
			if c.Continue != "" {
				t.Errorf("first call should have no Continue token, got %q", c.Continue)
			}
			continue
		}
		want := "page-token-" + string(rune('1'+i-1))
		if c.Continue != want {
			t.Errorf("call %d: Continue = %q, want %q", i, c.Continue, want)
		}
	}
}

func TestPaginatedSecretList_SinglePageNoContinue(t *testing.T) {
	calls := 0
	list := func(_ context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
		calls++
		return &corev1.SecretList{Items: []corev1.Secret{{ObjectMeta: metav1.ObjectMeta{Name: "only"}}}}, nil
	}
	got, err := paginatedSecretList(context.Background(), list, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || calls != 1 {
		t.Fatalf("expected exactly 1 item from exactly 1 call, got %d items from %d calls", len(got), calls)
	}
}

func TestPaginatedSecretList_ErrorMidPagination(t *testing.T) {
	calls := 0
	list := func(_ context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
		calls++
		if calls == 1 {
			return &corev1.SecretList{
				ListMeta: metav1.ListMeta{Continue: "next"},
				Items:    []corev1.Secret{{ObjectMeta: metav1.ObjectMeta{Name: "a"}}},
			}, nil
		}
		return nil, errors.New("transient API error on page 2")
	}
	_, err := paginatedSecretList(context.Background(), list, metav1.ListOptions{})
	if err == nil {
		t.Fatal("expected an error from the second page to propagate, got nil")
	}
	if calls != 2 {
		t.Fatalf("expected the loop to stop after the failing call, got %d calls", calls)
	}
}

func TestPaginatedSecretList_DefaultsLimitWhenUnset(t *testing.T) {
	var seenLimit int64 = -1
	list := func(_ context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
		seenLimit = opts.Limit
		return &corev1.SecretList{}, nil
	}
	if _, err := paginatedSecretList(context.Background(), list, metav1.ListOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenLimit != defaultListPageSize {
		t.Fatalf("expected default Limit %d to be applied, got %d", defaultListPageSize, seenLimit)
	}
}

func TestPaginatedUnstructuredList_FollowsContinueAcrossPages(t *testing.T) {
	pages := [][]unstructured.Unstructured{
		{{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": "app-a"}}}},
		{{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": "app-b"}}}},
	}
	calls := 0
	list := func(_ context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		i := calls
		calls++
		l := &unstructured.UnstructuredList{Items: pages[i]}
		if i == 0 {
			l.SetContinue("page-2")
		}
		return l, nil
	}

	got, err := paginatedUnstructuredList(context.Background(), list, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 accumulated items across 2 pages, got %d", len(got))
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 List() calls, got %d", calls)
	}
	if got[0].GetName() != "app-a" || got[1].GetName() != "app-b" {
		t.Fatalf("items out of order or wrong: %+v", got)
	}
}

func TestPaginatedUnstructuredList_SinglePageNoContinue(t *testing.T) {
	calls := 0
	list := func(_ context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		calls++
		return &unstructured.UnstructuredList{
			Items: []unstructured.Unstructured{{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": "only"}}}},
		}, nil
	}
	got, err := paginatedUnstructuredList(context.Background(), list, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || calls != 1 {
		t.Fatalf("expected exactly 1 item from exactly 1 call, got %d items from %d calls", len(got), calls)
	}
}
