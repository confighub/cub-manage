// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const policy = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: web
  namespace: shop
spec:
  podSelector: {}
  ingress:
    - from:
        - podSelector: {}
    - ports:
        - port: 80
  egress:
    - to:
        - ipBlock:
            cidr: 10.0.0.0/8
    - to:
        - podSelector: {}
        - ipBlock:
            cidr: 0.0.0.0/0
`

// The NetworkPolicy rules name the rule and the peer that fail, by index.
func TestNetpolRulesNameTheFailingRule(t *testing.T) {
	result := celruletest.Eval(t, celNoAllowAllIngress, policy)
	celruletest.Expect(t, result, "spec.ingress.1.from")
	celruletest.ExpectResource(t, result, "shop/web", "networking.k8s.io/v1/NetworkPolicy")

	celruletest.Expect(t, celruletest.Eval(t, celNoWideCidrEgress, policy), "spec.egress.1.to.1.ipBlock.cidr")
	// 0.0.0.0/0 contains the metadata IP, and nothing excepts it.
	celruletest.Expect(t, celruletest.Eval(t, celNoMetadataEgress, policy), "spec.egress.1.to.1.ipBlock.except")

	excepted := `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: web
spec:
  podSelector: {}
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: ["169.254.169.254/32"]
`
	celruletest.Expect(t, celruletest.Eval(t, celNoMetadataEgress, excepted))
	celruletest.Expect(t, celruletest.Eval(t, celNoAllowAllIngress, excepted))

	annotated := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations:\n    netpol.confighub.com/ingress-coverage: uncovered\n"
	celruletest.Expect(t, celruletest.Eval(t, celNoCoverageFinding, annotated), "metadata.annotations.netpol~1confighub~1com/ingress-coverage")
	celruletest.Expect(t, celruletest.Eval(t, celNoWideCidrEgress, annotated))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}

const egressPolicy = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: run
  namespace: agents
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to: []
      ports:
        - port: 53
          protocol: UDP
    - to: []
      ports:
        - port: 443
    - {}
    - to:
        - ipBlock:
            cidr: ::/0
    - ports:
        - port: 80
`

func TestOpenEgress(t *testing.T) {
	// Rule 2 names nothing; rule 0 and 1 name ports, so they say what they allow.
	celruletest.Expect(t, celruletest.Eval(t, celNoOpenEgress, egressPolicy), "spec.egress.2.to")
	celruletest.Expect(t, celruletest.Eval(t, celNoWideCidrEgress, egressPolicy), "spec.egress.3.to.0.ipBlock.cidr")
}

func TestMetadataThroughOpenEgress(t *testing.T) {
	// Rules 2 (every port) and 4 (TCP 80) name no destinations and reach the
	// metadata service; 0 (UDP 53) and 1 (TCP 443) do not.
	celruletest.Expect(t, celruletest.Eval(t, celNoMetadataEgress, egressPolicy), "spec.egress.2.to", "spec.egress.4.to")
}

func TestEgressAllowsDNS(t *testing.T) {
	policy := func(policyTypes, egress string) string {
		return "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: p\nspec:\n  podSelector: {}\n" + policyTypes + egress
	}
	const toDB = "    - to:\n        - podSelector:\n            matchLabels:\n              app: db\n      ports:\n        - port: 5432\n"
	cases := []struct {
		name   string
		policy string
		fails  bool
	}{
		{"allows UDP 53 anywhere", egressPolicy, false},
		{"allows only a database", policy("", "  egress:\n"+toDB), true},
		// A port with no protocol is TCP, which most lookups do not use.
		{"allows TCP 53 only", policy("", "  egress:\n    - ports:\n        - port: 53\n"), true},
		{"allows UDP 53 to kube-system", policy("", "  egress:\n"+toDB+
			"    - to:\n        - namespaceSelector:\n            matchLabels:\n              kubernetes.io/metadata.name: kube-system\n      ports:\n        - port: 53\n          protocol: UDP\n"), false},
		{"allows UDP 53 within its namespace only", policy("", "  egress:\n    - to:\n        - podSelector: {}\n      ports:\n        - port: 53\n          protocol: UDP\n"), true},
		{"allows the named dns port", policy("", "  egress:\n    - ports:\n        - port: dns\n          protocol: UDP\n"), false},
		{"allows all UDP", policy("", "  egress:\n    - ports:\n        - protocol: UDP\n"), false},
		{"allows everything", policy("", "  egress:\n    - {}\n"), false},
		{"denies all egress deliberately", policy("  policyTypes: [Egress]\n", ""), false},
		{"isolates only ingress", policy("  policyTypes: [Ingress]\n", "  egress:\n"+toDB), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := celruletest.Eval(t, celEgressAllowsDNS, c.policy)
			if c.fails {
				celruletest.Expect(t, result, "spec.egress")
			} else {
				celruletest.Expect(t, result)
			}
		})
	}
}
