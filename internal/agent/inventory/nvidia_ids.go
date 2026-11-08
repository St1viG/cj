package inventory

import "fmt"

// gpuModel is what the agent reports for an NVIDIA PCI device id.
type gpuModel struct {
	Name   string
	MemMiB int64
}

// nvidiaModels maps PCI device ids (vendor 10de) to marketing names. It
// covers the demo GPUs and common datacenter parts; unknown ids are reported
// as "NVIDIA 10de:<id>". Add the laptop RTX here once spike S2b has
// identified it (roadmap P1.3 / P1.7).
var nvidiaModels = map[string]gpuModel{
	// Pascal: FED-D's GPU and its siblings.
	"1c02": {"GeForce GTX 1060 3GB", 3072},
	"1c03": {"GeForce GTX 1060 6GB", 6144},
	"1c20": {"GeForce GTX 1060 Mobile", 6144},
	"1b81": {"GeForce GTX 1070", 8192},
	"1b80": {"GeForce GTX 1080", 8192},
	"1b06": {"GeForce GTX 1080 Ti", 11264},
	// Consumer Ampere / Ada.
	"2204": {"GeForce RTX 3090", 24576},
	"2684": {"GeForce RTX 4090", 24576},
	// Datacenter.
	"1db4": {"Tesla V100 PCIe 16GB", 16384},
	"1db6": {"Tesla V100 PCIe 32GB", 32768},
	"1eb8": {"Tesla T4", 16384},
	"20b0": {"A100 SXM4 40GB", 40960},
	"20b2": {"A100 SXM4 80GB", 81920},
	"20b5": {"A100 PCIe 80GB", 81920},
	"20f1": {"A100 PCIe 40GB", 40960},
	"2330": {"H100 SXM5 80GB", 81920},
	"2331": {"H100 PCIe 80GB", 81920},
	"26b5": {"L40", 49152},
	"26b9": {"L40S", 49152},
	"27b8": {"L4", 24576},
}

// NVIDIAModel returns the model name and memory for a device id such as
// "1c03"; memory is 0 when unknown.
func NVIDIAModel(deviceID string) (string, int64) {
	if m, ok := nvidiaModels[deviceID]; ok {
		return m.Name, m.MemMiB
	}
	return fmt.Sprintf("NVIDIA 10de:%s", deviceID), 0
}
