// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package regolibrary reads the framework definitions taken from Kubescape's regolibrary, which are
// licensed under the Apache License 2.0; see LICENSE and README.md beside this file.
package regolibrary

import (
	"embed"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/confighub/sdk/managerkit/guardrails"
)

// Commit is the commit of kubescape/regolibrary the files were taken from.
const Commit = "7620eefd5cd2b7d3bc88122563091f521151e58d"

//go:embed frameworks/*.json
var files embed.FS

// framework is a framework file, of which only the controls are read.
type framework struct {
	ActiveControls []struct {
		ControlID string `json:"controlID"`
		Patch     struct {
			Name string `json:"name"`
		} `json:"patch"`
	} `json:"activeControls"`
}

// frameworks are the files, by the name --framework takes, with their titles. A CIS benchmark
// numbers its controls; genericCIS is the one whose numbers checks name as CIS:<number>.
var frameworks = []struct {
	name, file, title string
	genericCIS        bool
}{
	{"cis-v1.12", "cis-v1.12.0", "CIS Kubernetes Benchmark v1.12.0", true},
	{"cis-eks-v1.8", "cis-eks-t1.8.0", "CIS Amazon EKS Benchmark v1.8.0", false},
	{"cis-aks-v1.8", "cis-aks-t1.8.0", "CIS Azure AKS Benchmark v1.8.0", false},
	{"cis-gke-v1.9", "cis-gke-v1.9.0", "CIS Google GKE Benchmark v1.9.0", false},
	{"nsa", "nsaframework", "NSA and CISA Kubernetes Hardening Guidance", false},
	{"mitre", "mitre", "MITRE ATT&CK for Kubernetes, as Microsoft's threat matrix maps it", false},
	{"soc2", "soc2", "SOC 2", false},
}

var markdownEscape = regexp.MustCompile(`\\([\\_*\x60\[\]])`)

var cisControl = regexp.MustCompile(`^CIS-([0-9.]+[0-9])\s+(.*)$`)

// Frameworks are the frameworks, each control mapped to the Kubescape controls that implement it,
// as KS:<control>, and a generic CIS control also to CIS:<number>. Kubescape controls a CIS
// benchmark maps to one of its numbers are one control.
func Frameworks() []guardrails.Framework {
	out := make([]guardrails.Framework, 0, len(frameworks))
	for _, f := range frameworks {
		data, err := files.ReadFile("frameworks/" + f.file + ".json")
		if err != nil {
			panic(err)
		}
		var parsed framework
		if err := json.Unmarshal(data, &parsed); err != nil {
			panic("framework " + f.file + ": " + err.Error())
		}
		fw := guardrails.Framework{Name: f.name, Title: f.title, Source: "kubescape/regolibrary@" + Commit + ":frameworks/" + f.file + ".json", License: "Apache-2.0"}
		byID := map[string]int{}
		for _, c := range parsed.ActiveControls {
			// The names are Markdown, escaping underscores and the like.
			id, title := c.ControlID, markdownEscape.ReplaceAllString(c.Patch.Name, "$1")
			corresponds := []string{"KS:" + c.ControlID}
			if m := cisControl.FindStringSubmatch(title); m != nil {
				id, title = m[1], m[2]
				if f.genericCIS {
					corresponds = append(corresponds, "CIS:"+id)
				}
			}
			if i, ok := byID[id]; ok {
				for _, s := range corresponds {
					if !slices.Contains(fw.Controls[i].Corresponds, s) {
						fw.Controls[i].Corresponds = append(fw.Controls[i].Corresponds, s)
					}
				}
				continue
			}
			byID[id] = len(fw.Controls)
			fw.Controls = append(fw.Controls, guardrails.FrameworkControl{ID: id, Title: title, Corresponds: corresponds})
		}
		slices.SortFunc(fw.Controls, func(a, b guardrails.FrameworkControl) int { return compareIDs(a.ID, b.ID) })
		out = append(out, fw)
	}
	return out
}

// compareIDs orders control numbers part by part, numerically where a part is a number, so 5.2.10
// follows 5.2.9 and C-0100 follows C-0099.
func compareIDs(a, b string) int {
	pa, pb := strings.FieldsFunc(a, isSeparator), strings.FieldsFunc(b, isSeparator)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			return na - nb
		case (ea != nil || eb != nil) && pa[i] != pb[i]:
			return strings.Compare(pa[i], pb[i])
		}
	}
	return len(pa) - len(pb)
}

func isSeparator(r rune) bool { return r == '.' || r == '-' }
