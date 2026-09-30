package kube

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/runsafe"
)

// NodeDisk is how full a node's disk is.
//
// Read from the kubelet's own summary, through the API server's node proxy,
// because it is the kubelet that evicts everything on a node whose disk runs
// low, and it measures "low" as available bytes. Used here is capacity minus
// available, so a percentage shown in the panel is the one the kubelet acts on
// — reserved blocks included, which is why it can differ from `df`.
type NodeDisk struct {
	UsedMB     int64
	CapacityMB int64
}

// nodeDiskTimeout bounds one node's answer. A node that does not answer is
// already reported as not ready; waiting on it would hold up every other.
const nodeDiskTimeout = 5 * time.Second

// NodeDisks reads every node's disk, at once. A node that does not answer is
// missing from the result rather than failing the rest.
func (c *Client) NodeDisks(ctx context.Context) (map[string]NodeDisk, error) {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := map[string]NodeDisk{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, node := range nodes.Items {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			// A node answering with something that trips the parser costs
			// that node's number, not the panel.
			defer runsafe.Recover(nil, "reading a node's disk", nil)
			lookup, cancel := context.WithTimeout(ctx, nodeDiskTimeout)
			defer cancel()
			raw, err := c.clientset.CoreV1().RESTClient().Get().
				AbsPath("/api/v1/nodes", name, "proxy", "stats", "summary").
				DoRaw(lookup)
			if err != nil {
				return
			}
			if disk, ok := parseNodeDisk(raw); ok {
				mu.Lock()
				out[name] = disk
				mu.Unlock()
			}
		}(node.Name)
	}
	wg.Wait()
	return out, nil
}

// parseNodeDisk reads the node's root filesystem out of a kubelet summary.
func parseNodeDisk(raw []byte) (NodeDisk, bool) {
	var summary struct {
		Node struct {
			Fs *struct {
				AvailableBytes *uint64 `json:"availableBytes"`
				CapacityBytes  *uint64 `json:"capacityBytes"`
			} `json:"fs"`
		} `json:"node"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return NodeDisk{}, false
	}
	fs := summary.Node.Fs
	if fs == nil || fs.CapacityBytes == nil || fs.AvailableBytes == nil || *fs.CapacityBytes == 0 ||
		*fs.AvailableBytes > *fs.CapacityBytes {
		return NodeDisk{}, false
	}
	const mb = 1024 * 1024
	return NodeDisk{
		UsedMB:     int64((*fs.CapacityBytes - *fs.AvailableBytes) / mb),
		CapacityMB: int64(*fs.CapacityBytes / mb),
	}, true
}
