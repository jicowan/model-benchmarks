package recommend

// MockHFClient is a test double for HFClientInterface.
type MockHFClient struct {
	FetchModelConfigFunc func(modelID, hfToken string) (*ModelConfig, error)
}

// FetchModelConfig calls the mock function if set, otherwise returns nil.
func (m *MockHFClient) FetchModelConfig(modelID, hfToken string) (*ModelConfig, error) {
	if m.FetchModelConfigFunc != nil {
		return m.FetchModelConfigFunc(modelID, hfToken)
	}
	return nil, nil
}
