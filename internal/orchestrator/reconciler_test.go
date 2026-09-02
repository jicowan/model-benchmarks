package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// PRD-68 P6: resource naming + leak reconciler.

func TestResourceNames(t *testing.T) {
	id := "0123456789abcdef-0000-0000-0000-000000000000"
	if got := modelNameFor(id); got != "bench-0123456789ab" {
		t.Fatalf("modelNameFor = %q", got)
	}
	if got := loadgenNameFor(id); got != "loadgen-0123456789ab" {
		t.Fatalf("loadgenNameFor = %q", got)
	}
	if got := suiteLoadgenNameFor(id, "chatbot"); got != "loadgen-0123456789ab-chat" {
		t.Fatalf("suiteLoadgenNameFor = %q", got)
	}
	// Short ids (tests) must not panic.
	if got := modelNameFor("run-1"); got != "bench-run-1" {
		t.Fatalf("short id: %q", got)
	}
	// Longest generated name must fit the 63-char DNS label limit.
	if n := len(suiteLoadgenCMNameFor(id, "chatbot")); n > 63 {
		t.Fatalf("name too long: %d", n)
	}
}

func mkDeployment(name, runID string, age time.Duration) *appsv1.Deployment {
	labels := map[string]string{LabelRole: "model"}
	if runID != "" {
		labels[LabelRunID] = runID
	}
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: defaultNamespace, Labels: labels,
		CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
	}}
}

func TestReconcileLeaks(t *testing.T) {
	repo := database.NewMockRepo()
	ctx := context.Background()
	owner := "pod-a"
	live, _ := repo.CreateBenchmarkRun(ctx, &database.BenchmarkRun{ModelID: "m", InstanceTypeID: "i", Status: "running", OwnerPod: &owner})
	done, _ := repo.CreateBenchmarkRun(ctx, &database.BenchmarkRun{ModelID: "m", InstanceTypeID: "i", Status: "completed", OwnerPod: &owner})

	client := fake.NewSimpleClientset(
		mkDeployment("bench-live", live, time.Hour),              // live run → keep
		mkDeployment("bench-done", done, time.Hour),              // terminal run → delete
		mkDeployment("bench-gone", "deleted-run-id", time.Hour),  // no row → delete
		mkDeployment("bench-fresh", done, time.Minute),           // terminal but inside grace → keep
		mkDeployment("bench-legacy-old", "", 4*time.Hour),        // legacy, ancient → delete
		mkDeployment("bench-legacy-new", "", time.Hour),          // legacy, could be live → keep
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "loadgen-done", Namespace: defaultNamespace,
			Labels: map[string]string{LabelRole: "loadgen", LabelRunID: done},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "loadgen-config-done", Namespace: defaultNamespace,
			Labels: map[string]string{LabelRole: "loadgen-config", LabelRunID: done},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: defaultNamespace}},
	)
	o := New(client, repo, "pod-a")
	if err := o.ReconcileLeaksOnce(ctx); err != nil {
		t.Fatal(err)
	}

	deps, _ := client.AppsV1().Deployments(defaultNamespace).List(ctx, metav1.ListOptions{})
	got := map[string]bool{}
	for _, d := range deps.Items {
		got[d.Name] = true
	}
	for _, want := range []string{"bench-live", "bench-fresh", "bench-legacy-new"} {
		if !got[want] {
			t.Errorf("%s should have been kept", want)
		}
	}
	for _, gone := range []string{"bench-done", "bench-gone", "bench-legacy-old"} {
		if got[gone] {
			t.Errorf("%s should have been deleted", gone)
		}
	}
	if jobs, _ := client.BatchV1().Jobs(defaultNamespace).List(ctx, metav1.ListOptions{}); len(jobs.Items) != 0 {
		t.Errorf("loadgen job of terminal run not deleted")
	}
	cms, _ := client.CoreV1().ConfigMaps(defaultNamespace).List(ctx, metav1.ListOptions{})
	if len(cms.Items) != 1 || cms.Items[0].Name != "unrelated" {
		t.Errorf("expected only the unlabelled ConfigMap to survive, got %d", len(cms.Items))
	}
}
