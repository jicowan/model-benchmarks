package manifest

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// PRD-68 P1: yamlQuote must make every user-derived value a single YAML
// scalar, so a hostile hf_token / model id cannot append pod-spec fields.

func TestYAMLQuote_RoundTrips(t *testing.T) {
	cases := []string{
		"plain",
		"hf_abcDEF",
		`{"concurrency":32,"distributed":true}`, // inline JSON keeps single quotes
		"it's",
		`both ' and "`,
		"line1\nline2",
		"tab\there",
		"\"\n            securityContext:\n              privileged: true",
		"unicode ✓",
		"",
	}
	for _, in := range cases {
		q := YAMLQuote(in)
		var out string
		if err := yaml.Unmarshal([]byte(q), &out); err != nil {
			t.Errorf("%q → %s: not a valid YAML scalar: %v", in, q, err)
			continue
		}
		if out != in {
			t.Errorf("%q → %s → %q: round-trip mismatch", in, q, out)
		}
	}
}

func TestYAMLQuote_JSONExtraConfigStaysReadable(t *testing.T) {
	got := YAMLQuote(`{"concurrency":32}`)
	if got != `'{"concurrency":32}'` {
		t.Fatalf("expected single-quoted inline JSON, got %s", got)
	}
}

func TestRenderModelDeployment_TokenInjectionIsInert(t *testing.T) {
	// A token crafted to close the scalar and add a privileged securityContext.
	evil := "x\"\n          securityContext:\n            privileged: true\n          env:\n            - name: X\n              value: \""
	out, err := RenderModelDeployment(ModelDeploymentParams{
		Name: "bench-inj", Namespace: "accelbench", ModelHfID: "org/m",
		HfToken: evil, Framework: "vllm", FrameworkVersion: "v1",
		TensorParallelDegree: 1, AcceleratorType: "gpu", AcceleratorCount: 1,
		InstanceTypeName: "g5.xlarge", CPURequest: "1", MemoryRequest: "1Gi",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Decode the Deployment document and prove the injected text stayed inside
	// the HF_TOKEN value.
	doc := strings.SplitN(out, "\n---\n", 2)[0]
	var dep struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						SecurityContext map[string]any `json:"securityContext"`
						Env             []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal([]byte(doc), &dep); err != nil {
		t.Fatalf("rendered Deployment is not valid YAML: %v\n%s", err, doc)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if v, ok := c.SecurityContext["privileged"]; ok && v == true {
		t.Fatalf("injection succeeded: privileged=true\n%s", doc)
	}
	if c.SecurityContext["allowPrivilegeEscalation"] != false {
		t.Fatalf("allowPrivilegeEscalation must be false, got %v", c.SecurityContext["allowPrivilegeEscalation"])
	}
	found := false
	for _, e := range c.Env {
		if e.Name == "HF_TOKEN" {
			found = true
			if e.Value != evil {
				t.Fatalf("HF_TOKEN value altered: %q", e.Value)
			}
		}
	}
	if !found {
		t.Fatal("HF_TOKEN env missing")
	}
}
