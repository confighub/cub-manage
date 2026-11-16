// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package celadmission embeds the policies taken from Kubescape's cel-admission-library, which
// are licensed under the Apache License 2.0; see LICENSE and README.md beside this file.
package celadmission

import (
	"embed"
	"strings"
)

// Commit is the commit of kubescape/cel-admission-library the files were taken from.
const Commit = "a19021a73782eebbfc9736ac16381f1f11fcc2d7"

// Source names the repository and commit, for the library Unit's Source annotation.
const Source = "kubescape/cel-admission-library@" + Commit

//go:embed controls/*/policy.yaml configuration/basic-control-configuration.yaml
var files embed.FS

// Policies are the ValidatingAdmissionPolicies of the controls, such as C-0020, as YAML documents.
func Policies(controls ...string) string {
	docs := make([]string, len(controls))
	for i, control := range controls {
		data, err := files.ReadFile("controls/" + control + "/policy.yaml")
		if err != nil {
			panic("no policy for Kubescape control " + control)
		}
		docs[i] = strings.TrimSpace(string(data))
	}
	return strings.Join(docs, "\n---\n") + "\n"
}

// Params are the library's parameters, a ControlConfiguration named ParamsName, with its defaults.
func Params() string {
	data, err := files.ReadFile("configuration/basic-control-configuration.yaml")
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ParamsName is the name of the ControlConfiguration Params holds, which the bindings name.
const ParamsName = "basic-control-configuration"
