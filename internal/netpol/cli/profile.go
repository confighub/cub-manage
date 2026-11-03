// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The NetworkPolicy profile library: fixes to existing policies, as set-yq
// profiles in the shared library. Each one changes only the policies that need
// it, and changes nothing the second time.

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/spf13/cobra"

	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/profiles"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

// metadataIP is the cloud instance-metadata address.
const metadataIP = "169.254.169.254"

// allowDNSYQ adds a DNS allowance to kube-dns on port 53 to a policy that
// restricts egress and allows nothing on port 53, the allowance an egress
// default-deny needs not to break name resolution.
const allowDNSYQ = `(select(((.spec.policyTypes // []) | any_c(. == "Egress")) and ` +
	`([.spec.egress[]?.ports[]? | select(.port == 53)] | length) == 0) | .spec.egress) |= ` +
	`((. // []) + [{"to": [{"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}}, ` +
	`"podSelector": {"matchLabels": {"k8s-app": "kube-dns"}}}], "ports": [{"protocol": "UDP", "port": 53}, {"protocol": "TCP", "port": 53}]}])`

// exceptMetadataYQ excepts the metadata IP from every egress ipBlock whose CIDR
// contains it. An except outside its block's CIDR is invalid, so the blocks are
// matched by CIDR rather than all edited. Each step selects what exists rather
// than iterating with []?, which on the left of |= creates the path it walks:
// an empty egress list where there was none.
var exceptMetadataYQ = fmt.Sprintf(`(.spec | select(has("egress")) | .egress[] | select(has("to")) | .to[] | `+
	`select(has("ipBlock")) | .ipBlock | select(.cidr as $c | [%s] | any_c(. == $c))) |= `+
	`(.except = ((.except // []) + ["%s/32"] | unique))`, metadataCIDRs(), metadataIP)

// metadataCIDRs is every CIDR, written canonically, that contains the metadata
// IP, short of the /32 that names it alone, which allows it by intent.
func metadataCIDRs() string {
	ip := netip.MustParseAddr(metadataIP)
	cidrs := make([]string, 0, 32)
	for bits := 0; bits < 32; bits++ {
		cidrs = append(cidrs, fmt.Sprintf("%q", netip.PrefixFrom(ip, bits).Masked().String()))
	}
	return strings.Join(cidrs, ", ")
}

var networkPolicies = cub.WhereResourceTypes(cub.NetworkPolicyResourceType)

// Library is this command group's profile library. Its profiles are installed
// with every other group's by the top-level 'profile install'.
var Library = profiles.Library{
	Tool:           "cub-manage",
	Target:         "a NetworkPolicy Unit",
	Preflight:      cub.Preflight,
	InvocationName: kit.RootName,
	FleetEdit: profiles.FleetEdit{
		WhereData: "kind = 'NetworkPolicy'",
		Scope:     "NetworkPolicy Units",
		Example:   `fleet-edit --profile netpol-except-metadata --environment prod --commit --change-desc "…"`,
	},
	Profiles: []profiles.Spec{
		{
			Slug:          "netpol-allow-dns",
			Description:   "set-yq: allow DNS to kube-dns on port 53 from a policy that restricts egress",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", allowDNSYQ)},
			WhereResource: networkPolicies,
		},
		{
			Slug:          "netpol-except-metadata",
			Description:   "set-yq: except the cloud metadata IP from egress ipBlocks that contain it",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", exceptMetadataYQ)},
			WhereResource: networkPolicies,
		},
	},
}

func newFleetEditCmd() *cobra.Command { return Library.FleetEditCommand() }
