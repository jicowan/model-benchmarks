// PRD-33: Capacity Reservations (ODCRs + Capacity Blocks for ML) management.
//
// Operators attach EC2 capacity reservations to the GPU and Neuron Karpenter
// EC2NodeClasses from the Configuration page. The authoritative state is the
// NodeClass's `spec.capacityReservationSelectorTerms` field; live reservation
// metadata comes from ec2:DescribeCapacityReservations with a short cache.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// EC2Client is the subset of the EC2 SDK we need. Defined as an interface so
// tests can substitute a fake.
type EC2Client interface {
	DescribeCapacityReservations(ctx context.Context, in *ec2.DescribeCapacityReservationsInput, opts ...func(*ec2.Options)) (*ec2.DescribeCapacityReservationsOutput, error)
}

// DynamicClient is the subset of client-go's dynamic.Interface we use.
// Tests pass a fake dynamic.Interface from client-go/dynamic/fake.
type DynamicClient = dynamic.Interface

// Hard-coded NodeClass/NodePool mapping for AccelBench. Both happen to share
// the same name per resource kind. This list is what the GET response
// enumerates.
var reservationNodePools = []nodePoolConfig{
	{NodeClass: "gpu", NodePool: "gpu"},
	{NodeClass: "neuron", NodePool: "neuron"},
}

type nodePoolConfig struct {
	NodeClass string
	NodePool  string
}

var (
	gvrEC2NodeClass = schema.GroupVersionResource{Group: "karpenter.k8s.aws", Version: "v1", Resource: "ec2nodeclasses"}
	gvrNodePool     = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
)

// --- Response types ---------------------------------------------------------

type reservationSummary struct {
	ID                     string            `json:"id"`
	Type                   string            `json:"type"`  // "default" | "capacity-block"
	State                  string            `json:"state"` // active | scheduled | expired | ...
	InstanceType           string            `json:"instance_type"`
	AvailabilityZone       string            `json:"availability_zone"`
	TotalInstanceCount     int32             `json:"total_instance_count"`
	AvailableInstanceCount int32             `json:"available_instance_count"`
	StartDate              *time.Time        `json:"start_date,omitempty"`
	EndDate                *time.Time        `json:"end_date,omitempty"`
	EndDateType            string            `json:"end_date_type,omitempty"` // "limited" | "unlimited"
	DrainWarningAt         *time.Time        `json:"drain_warning_at,omitempty"`
	Tags                   map[string]string `json:"tags,omitempty"`
}

type nodePoolReservations struct {
	NodeClass                    string               `json:"node_class"`
	NodePool                     string               `json:"node_pool"`
	InstanceFamilies             []string             `json:"instance_families"`
	SubnetAZs                    []string             `json:"subnet_azs"`
	CapacityTypeIncludesReserved bool                 `json:"capacity_type_includes_reserved"`
	Reservations                 []reservationSummary `json:"reservations"`
}

// --- Describe cache (30s) ---------------------------------------------------

type describeCacheEntry struct {
	res []ec2types.CapacityReservation
	at  time.Time
}

var (
	describeCacheMu sync.Mutex
	describeCache   = map[string]describeCacheEntry{}
	describeCacheTTL = 30 * time.Second
)

// describeReservations is a thin caching wrapper around ec2:DescribeCapacityReservations.
// Cache key is the sorted ID list. For a card that renders on each page load
// this cuts EC2 calls to one per 30s regardless of browser refreshes.
func (s *Server) describeReservations(ctx context.Context, ids []string) ([]ec2types.CapacityReservation, error) {
	if s.ec2Client == nil {
		return nil, fmt.Errorf("ec2 client not configured")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	key := strings.Join(sortedCopy(ids), ",")
	describeCacheMu.Lock()
	if e, ok := describeCache[key]; ok && time.Since(e.at) < describeCacheTTL {
		out := e.res
		describeCacheMu.Unlock()
		return out, nil
	}
	describeCacheMu.Unlock()

	out, err := s.ec2Client.DescribeCapacityReservations(ctx, &ec2.DescribeCapacityReservationsInput{
		CapacityReservationIds: ids,
	})
	if err != nil {
		return nil, err
	}
	describeCacheMu.Lock()
	describeCache[key] = describeCacheEntry{res: out.CapacityReservations, at: time.Now()}
	describeCacheMu.Unlock()
	return out.CapacityReservations, nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// --- GET /api/v1/config/capacity-reservations -------------------------------

func (s *Server) handleListReservations(w http.ResponseWriter, r *http.Request) {
	if s.dynClient == nil || s.ec2Client == nil {
		writeError(w, http.StatusInternalServerError, "capacity reservations not configured")
		return
	}
	ctx := r.Context()

	out := make([]nodePoolReservations, 0, len(reservationNodePools))
	for _, np := range reservationNodePools {
		entry := nodePoolReservations{NodeClass: np.NodeClass, NodePool: np.NodePool}

		nc, err := s.dynClient.Resource(gvrEC2NodeClass).Get(ctx, np.NodeClass, metav1.GetOptions{})
		if err != nil {
			// If a NodeClass is absent (e.g. a cluster without neuron),
			// skip rather than 500 — degrade the card gracefully.
			continue
		}
		entry.SubnetAZs = subnetAZsFromNodeClass(nc)
		ids := reservationIDsFromNodeClass(nc)

		pool, err := s.dynClient.Resource(gvrNodePool).Get(ctx, np.NodePool, metav1.GetOptions{})
		if err == nil {
			entry.InstanceFamilies = instanceFamiliesFromNodePool(pool)
			entry.CapacityTypeIncludesReserved = capacityTypeIncludesReserved(pool)
		}

		if len(ids) > 0 {
			live, err := s.describeReservations(ctx, ids)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "describe capacity reservations: "+err.Error())
				return
			}
			foundByID := make(map[string]*ec2types.CapacityReservation, len(live))
			for i := range live {
				id := stringPtrValue(live[i].CapacityReservationId)
				foundByID[id] = &live[i]
			}
			for _, id := range ids {
				if cr, ok := foundByID[id]; ok {
					entry.Reservations = append(entry.Reservations, summarizeReservation(cr))
				} else {
					// EC2 didn't return data for this ID (access denied, wrong
					// region, etc.) — still surface it so operators can remove
					// stale entries from the selector list.
					entry.Reservations = append(entry.Reservations, reservationSummary{
						ID:    id,
						State: "unknown",
					})
				}
			}
		}

		out = append(out, entry)
	}

	writeJSON(w, http.StatusOK, out)
}

// --- POST /api/v1/config/capacity-reservations ------------------------------

type postReservationRequest struct {
	NodeClass     string `json:"node_class"`
	ReservationID string `json:"reservation_id"`
}

func (s *Server) handlePostReservation(w http.ResponseWriter, r *http.Request) {
	if s.dynClient == nil || s.ec2Client == nil {
		writeError(w, http.StatusInternalServerError, "capacity reservations not configured")
		return
	}
	var req postReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.NodeClass = strings.TrimSpace(req.NodeClass)
	req.ReservationID = strings.TrimSpace(req.ReservationID)
	if req.NodeClass == "" || req.ReservationID == "" {
		writeError(w, http.StatusBadRequest, "node_class and reservation_id are required")
		return
	}

	np, ok := findNodePoolConfig(req.NodeClass)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown node_class %q (must be one of: %s)",
			req.NodeClass, strings.Join(knownNodeClasses(), ", ")))
		return
	}

	ctx := r.Context()

	// Live-validate the reservation against EC2.
	live, err := s.ec2Client.DescribeCapacityReservations(ctx, &ec2.DescribeCapacityReservationsInput{
		CapacityReservationIds: []string{req.ReservationID},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "describe reservation: "+err.Error())
		return
	}
	if len(live.CapacityReservations) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("reservation %s not found", req.ReservationID))
		return
	}
	cr := &live.CapacityReservations[0]
	state := string(cr.State)
	switch state {
	case "cancelled", "expired", "failed", "payment-failed":
		writeError(w, http.StatusBadRequest, fmt.Sprintf("reservation is %s, cannot attach", state))
		return
	}
	if cr.EndDate != nil && cr.EndDate.Before(time.Now()) {
		writeError(w, http.StatusBadRequest, "reservation has ended")
		return
	}

	// Load NodeClass + NodePool for further validation and eventual patching.
	nc, err := s.dynClient.Resource(gvrEC2NodeClass).Get(ctx, np.NodeClass, metav1.GetOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get EC2NodeClass: "+err.Error())
		return
	}
	azs := subnetAZsFromNodeClass(nc)
	az := stringPtrValue(cr.AvailabilityZone)
	if !containsString(azs, az) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"reservation AZ %s not in EC2NodeClass subnet AZs [%s]", az, strings.Join(azs, ", ")))
		return
	}

	instType := stringPtrValue(cr.InstanceType)
	family := instanceFamilyOf(instType)
	pool, err := s.dynClient.Resource(gvrNodePool).Get(ctx, np.NodePool, metav1.GetOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get NodePool: "+err.Error())
		return
	}
	allowedFamilies := instanceFamiliesFromNodePool(pool)
	if !containsString(allowedFamilies, family) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"instance family %s not allowed by NodePool %s (allowed: %s)",
			family, np.NodePool, strings.Join(allowedFamilies, ", ")))
		return
	}

	// Duplicate check.
	existing := reservationIDsFromNodeClass(nc)
	if containsString(existing, req.ReservationID) {
		writeError(w, http.StatusConflict, "reservation already attached")
		return
	}

	// Patch NodeClass: append to capacityReservationSelectorTerms.
	if err := s.patchNodeClassAddReservation(ctx, np.NodeClass, req.ReservationID, existing); err != nil {
		writeError(w, http.StatusInternalServerError, "patch EC2NodeClass: "+err.Error())
		return
	}

	// Patch NodePool if capacity-type doesn't yet include "reserved".
	if !capacityTypeIncludesReserved(pool) {
		if err := s.patchNodePoolIncludeReserved(ctx, np.NodePool, pool); err != nil {
			writeError(w, http.StatusInternalServerError, "patch NodePool capacity-type: "+err.Error())
			return
		}
	}

	// Bust the describe cache so the next GET reflects the new ID.
	describeCacheMu.Lock()
	describeCache = map[string]describeCacheEntry{}
	describeCacheMu.Unlock()

	s.audit(ctx, "POST /api/v1/config/capacity-reservations",
		fmt.Sprintf("attached %s (%s, %s) to %s", req.ReservationID, instType, az, np.NodeClass))

	w.WriteHeader(http.StatusCreated)
}

// --- DELETE /api/v1/config/capacity-reservations/{node_class}/{reservation_id}

func (s *Server) handleDeleteReservation(w http.ResponseWriter, r *http.Request) {
	if s.dynClient == nil {
		writeError(w, http.StatusInternalServerError, "capacity reservations not configured")
		return
	}
	nodeClass := r.PathValue("node_class")
	id := r.PathValue("reservation_id")
	if _, ok := findNodePoolConfig(nodeClass); !ok {
		writeError(w, http.StatusBadRequest, "unknown node_class: "+nodeClass)
		return
	}
	ctx := r.Context()

	nc, err := s.dynClient.Resource(gvrEC2NodeClass).Get(ctx, nodeClass, metav1.GetOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get EC2NodeClass: "+err.Error())
		return
	}
	existing := reservationIDsFromNodeClass(nc)
	if !containsString(existing, id) {
		writeError(w, http.StatusNotFound, "reservation not attached to this NodeClass")
		return
	}
	remaining := make([]string, 0, len(existing)-1)
	for _, x := range existing {
		if x != id {
			remaining = append(remaining, x)
		}
	}
	if err := s.replaceReservationSelector(ctx, nodeClass, remaining); err != nil {
		writeError(w, http.StatusInternalServerError, "patch EC2NodeClass: "+err.Error())
		return
	}

	// Intentionally leave NodePool capacity-type alone. If any reservations
	// remain attached to other NodeClasses (or this one), "reserved" is still
	// useful. If none do, Karpenter simply doesn't select reserved — no harm.
	describeCacheMu.Lock()
	describeCache = map[string]describeCacheEntry{}
	describeCacheMu.Unlock()

	s.audit(ctx, fmt.Sprintf("DELETE /api/v1/config/capacity-reservations/%s/%s", nodeClass, id), "removed")

	w.WriteHeader(http.StatusNoContent)
}

// --- Patch helpers ----------------------------------------------------------

// patchNodeClassAddReservation appends a new {id: ...} entry to the
// capacityReservationSelectorTerms list. Uses a merge patch that rewrites the
// whole list because JSON Merge Patch (RFC 7396) cannot express "append to
// array".
func (s *Server) patchNodeClassAddReservation(ctx context.Context, name, newID string, existing []string) error {
	next := append(append([]string{}, existing...), newID)
	return s.replaceReservationSelector(ctx, name, next)
}

func (s *Server) replaceReservationSelector(ctx context.Context, name string, ids []string) error {
	terms := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		terms = append(terms, map[string]any{"id": id})
	}
	body := map[string]any{
		"spec": map[string]any{
			"capacityReservationSelectorTerms": terms,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = s.dynClient.Resource(gvrEC2NodeClass).Patch(ctx, name, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-api"})
	return err
}

// patchNodePoolIncludeReserved replaces the full requirements list with one
// where the karpenter.sh/capacity-type entry's `values` contains "reserved".
// JSON Merge Patch can't target list elements, so we rewrite the array.
func (s *Server) patchNodePoolIncludeReserved(ctx context.Context, name string, pool *unstructured.Unstructured) error {
	reqs := requirementsFromNodePool(pool)
	changed := false
	for i, r := range reqs {
		key, _ := r["key"].(string)
		if key != "karpenter.sh/capacity-type" {
			continue
		}
		values, _ := r["values"].([]any)
		hasReserved := false
		for _, v := range values {
			if s, ok := v.(string); ok && s == "reserved" {
				hasReserved = true
				break
			}
		}
		if !hasReserved {
			reqs[i]["values"] = append([]any{"reserved"}, values...)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	body := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"requirements": reqs,
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = s.dynClient.Resource(gvrNodePool).Patch(ctx, name, types.MergePatchType, raw,
		metav1.PatchOptions{FieldManager: "accelbench-api"})
	return err
}

// --- Unstructured helpers ---------------------------------------------------

func reservationIDsFromNodeClass(u *unstructured.Unstructured) []string {
	terms, _, _ := unstructured.NestedSlice(u.Object, "spec", "capacityReservationSelectorTerms")
	out := []string{}
	for _, t := range terms {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	return out
}

func subnetAZsFromNodeClass(u *unstructured.Unstructured) []string {
	// AccelBench's NodeClasses select subnets by tag, so spec.subnetSelectorTerms
	// doesn't directly list AZs. Karpenter resolves them and writes the result
	// to status.subnets[*].zone at runtime.
	subs, _, _ := unstructured.NestedSlice(u.Object, "status", "subnets")
	seen := map[string]bool{}
	out := []string{}
	for _, s := range subs {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if z, ok := m["zone"].(string); ok && z != "" && !seen[z] {
			seen[z] = true
			out = append(out, z)
		}
	}
	return out
}

func requirementsFromNodePool(u *unstructured.Unstructured) []map[string]any {
	reqs, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "requirements")
	out := make([]map[string]any, 0, len(reqs))
	for _, r := range reqs {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func instanceFamiliesFromNodePool(u *unstructured.Unstructured) []string {
	for _, r := range requirementsFromNodePool(u) {
		if key, _ := r["key"].(string); key == "karpenter.k8s.aws/instance-family" {
			vs, _ := r["values"].([]any)
			out := make([]string, 0, len(vs))
			for _, v := range vs {
				if s, ok := v.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

func capacityTypeIncludesReserved(u *unstructured.Unstructured) bool {
	for _, r := range requirementsFromNodePool(u) {
		if key, _ := r["key"].(string); key == "karpenter.sh/capacity-type" {
			vs, _ := r["values"].([]any)
			for _, v := range vs {
				if s, ok := v.(string); ok && s == "reserved" {
					return true
				}
			}
		}
	}
	return false
}

// --- Misc helpers -----------------------------------------------------------

func summarizeReservation(cr *ec2types.CapacityReservation) reservationSummary {
	s := reservationSummary{
		ID:                     stringPtrValue(cr.CapacityReservationId),
		Type:                   string(cr.ReservationType),
		State:                  string(cr.State),
		InstanceType:           stringPtrValue(cr.InstanceType),
		AvailabilityZone:       stringPtrValue(cr.AvailabilityZone),
		TotalInstanceCount:     int32PtrValue(cr.TotalInstanceCount),
		AvailableInstanceCount: int32PtrValue(cr.AvailableInstanceCount),
		EndDateType:            string(cr.EndDateType),
	}
	if cr.StartDate != nil {
		t := *cr.StartDate
		s.StartDate = &t
	}
	if cr.EndDate != nil {
		t := *cr.EndDate
		s.EndDate = &t
		// Karpenter drains capacity-block nodes ~40 min before EC2's hard
		// termination. Surface that so operators can tell when the last
		// benchmark that'll finish on this reservation must be kicked off.
		if cr.ReservationType == ec2types.CapacityReservationTypeCapacityBlock {
			warn := t.Add(-40 * time.Minute)
			s.DrainWarningAt = &warn
		}
	}
	if len(cr.Tags) > 0 {
		s.Tags = map[string]string{}
		for _, tag := range cr.Tags {
			s.Tags[stringPtrValue(tag.Key)] = stringPtrValue(tag.Value)
		}
	}
	return s
}

func findNodePoolConfig(nodeClass string) (nodePoolConfig, bool) {
	for _, np := range reservationNodePools {
		if np.NodeClass == nodeClass {
			return np, true
		}
	}
	return nodePoolConfig{}, false
}

func knownNodeClasses() []string {
	out := make([]string, len(reservationNodePools))
	for i, np := range reservationNodePools {
		out[i] = np.NodeClass
	}
	return out
}

func instanceFamilyOf(instanceType string) string {
	i := strings.IndexByte(instanceType, '.')
	if i < 0 {
		return instanceType
	}
	return instanceType[:i]
}

func stringPtrValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func int32PtrValue(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func containsString(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}
