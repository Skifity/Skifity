package kube

import "testing"

func TestADiskIsReadAsTheKubeletMeasuresIt(t *testing.T) {
	// 40 GiB with 6 GiB available: 34 GiB used, reserved blocks included,
	// which is what the kubelet's eviction threshold is measured against.
	raw := []byte(`{"node": {"nodeName": "web-1", "fs": {
		"availableBytes": 6442450944, "capacityBytes": 42949672960, "usedBytes": 30064771072
	}}, "pods": []}`)
	disk, ok := parseNodeDisk(raw)
	if !ok || disk.CapacityMB != 40960 || disk.UsedMB != 34816 {
		t.Fatalf("read %+v, %v", disk, ok)
	}

	for name, bad := range map[string]string{
		"not JSON":         `<html>`,
		"no filesystem":    `{"node": {"nodeName": "web-1"}}`,
		"no capacity":      `{"node": {"fs": {"availableBytes": 10}}}`,
		"zero capacity":    `{"node": {"fs": {"availableBytes": 0, "capacityBytes": 0}}}`,
		"more than it has": `{"node": {"fs": {"availableBytes": 20, "capacityBytes": 10}}}`,
	} {
		if disk, ok := parseNodeDisk([]byte(bad)); ok {
			t.Errorf("%s was read as %+v", name, disk)
		}
	}
}
