# benchci
Run Go benchmarks as part of CI

Inspired by https://github.com/knqyf263/cob

## Example

### Example configuration

```yaml
command: go
benchtime: 10s
threshold: 0.1
compare: "ns/op,B/op"
cpu: 2
benchmem: true
timeout: 10m
benchmarks:
- name: "BenchmarkSyncAddressGroup"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  cpu: 1
- name: "BenchmarkInitXLargeScaleWithSmallNamespaces"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  benchtime: 20x
  cpu: 1
  threshold: 0.3
- name: "BenchmarkInitXLargeScaleWithLargeNamespaces"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  benchtime: 10x
  cpu: 1
- name: "BenchmarkInitXLargeScaleWithOneNamespace"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  benchtime: 10x
  cpu: 1
- name: "BenchmarkInitXLargeScaleWithNetpolPerPod"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  benchtime: 10x
  cpu: 1
- name: "BenchmarkInitXLargeScaleWithClusterScopedNetpol"
  package: "antrea.io/antrea/pkg/controller/networkpolicy"
  benchtime: 10x
  cpu: 1
- name: "BenchmarkCluster_ShouldSelect/Select_Node_from_1000_alive_Nodes-nodeSelectedForEgress"
  package: "antrea.io/antrea/pkg/agent/memberlist"
```

### Example usage

```bash
./bin/benchci -config c.yml
```
