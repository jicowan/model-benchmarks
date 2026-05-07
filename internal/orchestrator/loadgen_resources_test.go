package orchestrator

import "testing"

func TestLoadgenResources(t *testing.T) {
	tests := []struct {
		name        string
		workers     int
		wantCPUReq  string
		wantCPULim  string
		wantMemReq  string
		wantMemLim  string
	}{
		{"default 4", 4, "2", "4", "4496Mi", "8992Mi"},
		{"8 workers", 8, "2", "4", "4896Mi", "9792Mi"},
		{"16 workers", 16, "4", "8", "5696Mi", "11392Mi"},
		{"32 workers", 32, "8", "16", "7296Mi", "14592Mi"},
		{"64 workers", 64, "16", "32", "10496Mi", "20992Mi"},
		{"cap at 128", 128, "32", "64", "16896Mi", "33792Mi"},
		{"over cap clamps to 128", 500, "32", "64", "16896Mi", "33792Mi"},
		{"floor at 1", 0, "2", "4", "4196Mi", "8392Mi"},
		{"floor at 1 for negatives", -5, "2", "4", "4196Mi", "8392Mi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cpuReq, cpuLim, memReq, memLim := loadgenResources(tc.workers)
			if cpuReq != tc.wantCPUReq {
				t.Errorf("cpu req = %q, want %q", cpuReq, tc.wantCPUReq)
			}
			if cpuLim != tc.wantCPULim {
				t.Errorf("cpu lim = %q, want %q", cpuLim, tc.wantCPULim)
			}
			if memReq != tc.wantMemReq {
				t.Errorf("mem req = %q, want %q", memReq, tc.wantMemReq)
			}
			if memLim != tc.wantMemLim {
				t.Errorf("mem lim = %q, want %q", memLim, tc.wantMemLim)
			}
		})
	}
}
