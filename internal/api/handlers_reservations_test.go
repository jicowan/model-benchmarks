package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// --- Fakes ------------------------------------------------------------------

type fakeEC2 struct {
	// reservations returned keyed by ID. An unrecognized ID returns empty.
	byID map[string]ec2types.CapacityReservation
	// if set, DescribeCapacityReservations returns this error
	err error
}

func (f *fakeEC2) DescribeCapacityReservations(
	_ context.Context,
	in *ec2.DescribeCapacityReservationsInput,
	_ ...func(*ec2.Options),
) (*ec2.DescribeCapacityReservationsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := &ec2.DescribeCapacityReservationsOutput{}
	for _, id := range in.CapacityReservationIds {
		if cr, ok := f.byID[id]; ok {
			out.CapacityReservations = append(out.CapacityReservations, cr)
		}
	}
	return out, nil
}

// reservation constructs a test CapacityReservation with common defaults.
// Overrides can be applied via the opts closure.
func reservation(id, instanceType, az, state string, opts ...func(*ec2types.CapacityReservation)) ec2types.CapacityReservation {
	cr := ec2types.CapacityReservation{
		CapacityReservationId:  aws.String(id),
		InstanceType:           aws.String(instanceType),
		AvailabilityZone:       aws.String(az),
		State:                  ec2types.CapacityReservationState(state),
		ReservationType:        ec2types.CapacityReservationTypeDefault,
		TotalInstanceCount:     aws.Int32(2),
		AvailableInstanceCount: aws.Int32(2),
	}
	for _, opt := range opts {
		opt(&cr)
	}
	return cr
}

func gpuNodeClass() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.k8s.aws/v1",
		"kind":       "EC2NodeClass",
		"metadata":   map[string]any{"name": "gpu"},
		"spec": map[string]any{
			"amiSelectorTerms": []any{map[string]any{"alias": "al2023@latest"}},
		},
		"status": map[string]any{
			"subnets": []any{
				map[string]any{"id": "subnet-a", "zone": "us-east-2a"},
				map[string]any{"id": "subnet-b", "zone": "us-east-2b"},
				map[string]any{"id": "subnet-c", "zone": "us-east-2c"},
			},
		},
	}}
}

func gpuNodePool(capacityTypeValues ...any) *unstructured.Unstructured {
	if len(capacityTypeValues) == 0 {
		capacityTypeValues = []any{"on-demand"}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": "gpu"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"requirements": []any{
						map[string]any{
							"key":      "karpenter.k8s.aws/instance-family",
							"operator": "In",
							"values":   []any{"g5", "g6", "g6e", "p5", "p5e"},
						},
						map[string]any{
							"key":      "karpenter.sh/capacity-type",
							"operator": "In",
							"values":   capacityTypeValues,
						},
					},
				},
			},
		},
	}}
}

func neuronNodeClass() *unstructured.Unstructured {
	nc := gpuNodeClass()
	nc.Object["metadata"] = map[string]any{"name": "neuron"}
	return nc
}
func neuronNodePool() *unstructured.Unstructured {
	np := gpuNodePool("on-demand")
	np.Object["metadata"] = map[string]any{"name": "neuron"}
	// swap instance families
	spec := np.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	reqs := spec["requirements"].([]any)
	reqs[0] = map[string]any{
		"key":      "karpenter.k8s.aws/instance-family",
		"operator": "In",
		"values":   []any{"inf2", "trn1", "trn1n", "trn2"},
	}
	return np
}

// setupReservationsServer wires a Server with fake EC2 + dynamic K8s clients.
func setupReservationsServer(ec *fakeEC2, objs ...runtime.Object) (*database.MockRepo, *http.ServeMux) {
	repo := database.NewMockRepo()
	srv := NewServer(repo, k8sfake.NewSimpleClientset(), "test-pod")

	scheme := runtime.NewScheme()
	gvrToList := map[schema.GroupVersionResource]string{
		gvrEC2NodeClass: "EC2NodeClassList",
		gvrNodePool:     "NodePoolList",
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToList, objs...)
	srv.SetReservationsClients(ec, dyn)

	// Bust the describe cache between tests so cross-test state doesn't leak.
	describeCacheMu.Lock()
	describeCache = map[string]describeCacheEntry{}
	describeCacheMu.Unlock()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return repo, mux
}

// --- Tests ------------------------------------------------------------------

func TestReservations_List_EmptyState(t *testing.T) {
	_, mux := setupReservationsServer(
		&fakeEC2{},
		gpuNodeClass(), gpuNodePool(),
		neuronNodeClass(), neuronNodePool(),
	)

	req := httptest.NewRequest("GET", "/api/v1/config/capacity-reservations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp []nodePoolReservations
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Fatalf("expected 2 node pools, got %d", len(resp))
	}
	for _, np := range resp {
		if len(np.Reservations) != 0 {
			t.Errorf("%s should have no reservations, got %d", np.NodeClass, len(np.Reservations))
		}
		if len(np.SubnetAZs) != 3 {
			t.Errorf("%s should have 3 subnet AZs, got %d", np.NodeClass, len(np.SubnetAZs))
		}
	}
}

func TestReservations_Post_UnknownID(t *testing.T) {
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-missing"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReservations_Post_CancelledState(t *testing.T) {
	cr := reservation("cr-cancel", "p5.48xlarge", "us-east-2b", "cancelled")
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-cancel": cr}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-cancel"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cancelled") {
		t.Errorf("error message should mention cancelled state: %s", w.Body.String())
	}
}

func TestReservations_Post_WrongAZ(t *testing.T) {
	// NodeClass has us-east-2a/b/c; reservation is in us-west-2a.
	cr := reservation("cr-wrongaz", "p5.48xlarge", "us-west-2a", "active")
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-wrongaz": cr}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-wrongaz"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AZ") {
		t.Errorf("expected AZ error: %s", w.Body.String())
	}
}

func TestReservations_Post_WrongInstanceFamily(t *testing.T) {
	// GPU NodePool allows g5/g6/g6e/p5/p5e; m6i is not in the list.
	cr := reservation("cr-m6i", "m6i.xlarge", "us-east-2b", "active")
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-m6i": cr}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-m6i"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "m6i") {
		t.Errorf("expected family error mentioning m6i: %s", w.Body.String())
	}
}

func TestReservations_Post_DuplicateAttach(t *testing.T) {
	cr := reservation("cr-dup", "p5.48xlarge", "us-east-2b", "active")
	nc := gpuNodeClass()
	// Pre-attach cr-dup.
	nc.Object["spec"].(map[string]any)["capacityReservationSelectorTerms"] = []any{
		map[string]any{"id": "cr-dup"},
	}
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-dup": cr}},
		nc, gpuNodePool("reserved", "on-demand"),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-dup"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReservations_Post_SuccessAttachesAndUpdatesNodePool(t *testing.T) {
	cr := reservation("cr-good", "p5.48xlarge", "us-east-2b", "active")
	repo, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-good": cr}},
		gpuNodeClass(), gpuNodePool(), // capacity-type starts with only ["on-demand"]
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-good"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Verify audit log captured the attach.
	entries, _ := repo.ListAuditLog(context.Background(), 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Summary, "cr-good") ||
		!strings.Contains(entries[0].Summary, "p5.48xlarge") {
		t.Errorf("audit summary missing ID or instance type: %q", entries[0].Summary)
	}
}

func TestReservations_Post_SuccessOnExpiredCapacityBlock_Rejected(t *testing.T) {
	// A capacity block that has already ended should be rejected even if
	// state is still "active" (EC2 is eventually consistent on transitions).
	past := time.Now().Add(-2 * time.Hour)
	cr := reservation("cr-ended", "p5.48xlarge", "us-east-2b", "active", func(c *ec2types.CapacityReservation) {
		c.ReservationType = ec2types.CapacityReservationTypeCapacityBlock
		c.EndDateType = ec2types.EndDateTypeLimited
		c.EndDate = &past
	})
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-ended": cr}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-ended"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for expired CBR, got %d: %s", w.Code, w.Body.String())
	}
}

func TestReservations_Delete_RemovesID(t *testing.T) {
	cr := reservation("cr-keep", "p5.48xlarge", "us-east-2b", "active")
	nc := gpuNodeClass()
	nc.Object["spec"].(map[string]any)["capacityReservationSelectorTerms"] = []any{
		map[string]any{"id": "cr-keep"},
		map[string]any{"id": "cr-remove"},
	}
	repo, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-keep": cr}},
		nc, gpuNodePool("reserved", "on-demand"),
	)

	req := httptest.NewRequest("DELETE", "/api/v1/config/capacity-reservations/gpu/cr-remove", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	entries, _ := repo.ListAuditLog(context.Background(), 5)
	if len(entries) != 1 {
		t.Errorf("expected audit entry after delete, got %d", len(entries))
	}
}

func TestReservations_Delete_UnattachedReturns404(t *testing.T) {
	_, mux := setupReservationsServer(
		&fakeEC2{},
		gpuNodeClass(), gpuNodePool(),
	)

	req := httptest.NewRequest("DELETE", "/api/v1/config/capacity-reservations/gpu/cr-nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestReservations_List_CapacityBlockDrainWarning(t *testing.T) {
	end := time.Now().Add(24 * time.Hour)
	start := time.Now().Add(-1 * time.Hour)
	cr := reservation("cr-cbr", "p5.48xlarge", "us-east-2b", "active", func(c *ec2types.CapacityReservation) {
		c.ReservationType = ec2types.CapacityReservationTypeCapacityBlock
		c.EndDateType = ec2types.EndDateTypeLimited
		c.StartDate = &start
		c.EndDate = &end
	})
	nc := gpuNodeClass()
	nc.Object["spec"].(map[string]any)["capacityReservationSelectorTerms"] = []any{
		map[string]any{"id": "cr-cbr"},
	}
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-cbr": cr}},
		nc, gpuNodePool("reserved", "on-demand"),
		neuronNodeClass(), neuronNodePool(),
	)

	req := httptest.NewRequest("GET", "/api/v1/config/capacity-reservations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp []nodePoolReservations
	json.NewDecoder(w.Body).Decode(&resp)
	var cbr *reservationSummary
	for _, np := range resp {
		if np.NodeClass == "gpu" && len(np.Reservations) > 0 {
			cbr = &np.Reservations[0]
			break
		}
	}
	if cbr == nil {
		t.Fatal("expected reservation in response")
	}
	if cbr.Type != "capacity-block" {
		t.Errorf("type = %q, want capacity-block", cbr.Type)
	}
	if cbr.DrainWarningAt == nil {
		t.Fatal("expected drain_warning_at on capacity-block")
	}
	// Karpenter drains ~40 min early.
	expected := end.Add(-40 * time.Minute)
	if cbr.DrainWarningAt.Sub(expected).Abs() > time.Second {
		t.Errorf("drain_warning_at = %v, want %v (end - 40min)", cbr.DrainWarningAt, expected)
	}
}

func TestReservations_Post_MissingClients500(t *testing.T) {
	repo := database.NewMockRepo()
	srv := NewServer(repo, k8sfake.NewSimpleClientset(), "test-pod")
	// Do NOT call SetReservationsClients.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	body := strings.NewReader(`{"node_class":"gpu","reservation_id":"cr-x"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 with no clients configured, got %d", w.Code)
	}
}

func TestReservations_Post_UnknownNodeClass(t *testing.T) {
	cr := reservation("cr-x", "p5.48xlarge", "us-east-2b", "active")
	_, mux := setupReservationsServer(
		&fakeEC2{byID: map[string]ec2types.CapacityReservation{"cr-x": cr}},
		gpuNodeClass(), gpuNodePool(),
	)

	body := strings.NewReader(`{"node_class":"nonsense","reservation_id":"cr-x"}`)
	req := httptest.NewRequest("POST", "/api/v1/config/capacity-reservations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown node_class, got %d: %s", w.Code, w.Body.String())
	}
}

// verify helper functions

func TestInstanceFamilyOf(t *testing.T) {
	tests := map[string]string{
		"p5.48xlarge":   "p5",
		"g6e.xlarge":    "g6e",
		"m6i.16xlarge":  "m6i",
		"p6-b200.48xlarge": "p6-b200",
		"nodot":         "nodot",
	}
	for in, want := range tests {
		got := instanceFamilyOf(in)
		if got != want {
			t.Errorf("instanceFamilyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// Sanity check that the cache doesn't cause the GET to blow up when EC2 is
// called with an empty set (no attached reservations on either NodeClass).
func TestReservations_List_DescribeNotCalledWhenNoIDs(t *testing.T) {
	ec := &fakeEC2{err: fmt.Errorf("should not be called")}
	_, mux := setupReservationsServer(
		ec,
		gpuNodeClass(), gpuNodePool(),
		neuronNodeClass(), neuronNodePool(),
	)
	req := httptest.NewRequest("GET", "/api/v1/config/capacity-reservations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
