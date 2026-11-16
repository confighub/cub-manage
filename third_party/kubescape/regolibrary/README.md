# Kubescape regolibrary frameworks

Framework definitions from [kubescape/regolibrary](https://github.com/kubescape/regolibrary), at commit
`7620eefd5cd2b7d3bc88122563091f521151e58d`, licensed under the Apache License 2.0 (`LICENSE`, copied from the repository). The repository has
no NOTICE file. No Rego is taken.

Files taken, unmodified, from `frameworks/`: `cis-v1.12.0.json`, `cis-eks-t1.8.0.json`, `cis-aks-t1.8.0.json`,
`cis-gke-v1.9.0.json`, `nsaframework.json`, `mitre.json` and `soc2.json`.

Each maps a framework's controls to Kubescape's controls. `guardrails status --framework` reads that mapping,
with the Kubescape controls each guardrail check names in its `Corresponds`, to report how far the installed
rules go towards the framework. Only each control's ID, its name and the Kubescape control it maps to are read.

To add a framework, or move to a newer commit, copy the files from the same commit and update this README.
