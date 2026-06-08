package runtime

import (
	"fmt"
	"sort"
)

var registry = map[string]Runtime{}

func init() {
	Register(&VLLMgpu{})
	Register(&VLLMneuron{})
	Register(&SGLang{})
}

func Register(r Runtime) {
	registry[r.Name()] = r
}

// Get returns the Runtime for the given framework string.
func Get(framework string) (Runtime, error) {
	r, ok := registry[framework]
	if !ok {
		return nil, fmt.Errorf("framework %q is not supported (expected one of %v)", framework, Names())
	}
	return r, nil
}

// Names returns all registered framework strings in sorted order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ForAccelerator returns the default runtime for a given accelerator type.
func ForAccelerator(accelType string) Runtime {
	if accelType == "neuron" {
		return registry["vllm-neuron"]
	}
	return registry["vllm"]
}

// SupportsAccelerator checks if a runtime supports the given accelerator type.
func SupportsAccelerator(r Runtime, accelType string) bool {
	for _, a := range r.SupportedAccelerators() {
		if a == accelType {
			return true
		}
	}
	return false
}
