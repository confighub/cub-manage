# Kubescape cel-admission-library

ValidatingAdmissionPolicies from [kubescape/cel-admission-library](https://github.com/kubescape/cel-admission-library),
at commit `a19021a73782eebbfc9736ac16381f1f11fcc2d7`, licensed under the Apache License 2.0 (`LICENSE`, copied
from the repository). The repository has no NOTICE file.

The workload guardrail pack adopts these policies as checks on the `vap` engine: install writes them into the
policy Space as a library Unit, with a binding of each, and the library's parameters as a params Unit.

Files taken, unmodified:

- `controls/C-0020/`, `C-0062/`, `C-0081/`, `C-0292/`: each control's `policy.yaml`, and its
  `tests.json`, which the pack's tests run through `vet-validating-admission-policy`
- `configuration/basic-control-configuration.yaml`: the parameters, with the library's defaults
- `test-resources/`: the resources those tests start from (`cronjob.yaml`, `daemonset.yaml`, `deployment.yaml`,
  `deployment-for-list-items.yaml`, `pod.yaml`, `pod-for-list-items.yaml`, `pod-with-init-container.yaml`,
  `statefulset.yaml`),
  and the parameters they use (`default-control-configuration.yaml`, `empty-control-configuration.yaml`)

C-0075 (an image with no image tag or the latest one pulls Always) is not adopted: the API server defaults
`imagePullPolicy` to `Always` for those images before admission, and ConfigHub checks configuration as it is
written, so the policy would fail every container that relies on the default. `workload-explicit-image-tag` flags
those images already.

To adopt another control, or move to a newer commit, copy the files from the same commit and update this README.
